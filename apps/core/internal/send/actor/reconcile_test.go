package actor_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func seqsOf(groups [][]domain.Message) [][]uint64 {
	out := make([][]uint64, len(groups))
	for i, g := range groups {
		for _, m := range g {
			out[i] = append(out[i], m.Seq)
		}
	}
	return out
}

func assertGroupSeqs(t *testing.T, rg *rig, want ...[]uint64) {
	t.Helper()
	if got := seqsOf(rg.sub.sent()); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("group seqs = %v, want %v", got, want)
	}
}

func assertStoredOnce(t *testing.T, rg *rig, cid string, ack actor.Ack) {
	t.Helper()
	docs := storedCIDs(t, rg.msgs.Messages, roomA)[cid]
	if len(docs) != 1 {
		t.Fatalf("stored %d copies of %s, want 1", len(docs), cid)
	}
	assertAckMatches(t, ack, docs[0])
}

func TestUnknownWriteThatLandedIsConfirmedWithoutReinsert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(rg.sub.landedUnknown)
		rg.start(t)
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		assertGroupSeqs(t, rg, []uint64{1})
		assertStoredOnce(t, rg, "c1", ack)
		if _, _, finds := rg.msgs.counts(); finds != 1 {
			t.Fatalf("Find called %d times, want 1", finds)
		}
	})
}

func TestUnknownWriteThatIsAbsentIsRetriedAtTheSameSeq(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		lastLost := func(msgs []domain.Message) []store.Result {
			n := len(msgs) - 1
			return append(rg.sub.insert(msgs[:n]), lostUnknown(msgs[n:])...)
		}
		rg.sub.then(rg.sub.insert, lastLost)
		rg.start(t)
		ctx := t.Context()
		busy := sendAsync(ctx, rg.Router, cmd(roomA, "bob", "busy"))
		synctest.Wait()
		c1 := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1"))
		synctest.Wait()
		c2 := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c2"))
		synctest.Wait()
		rg.sub.release()
		synctest.Wait()
		c3 := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c3"))
		synctest.Wait()
		rg.sub.release()
		synctest.Wait()
		rg.sub.release()
		want := map[string]uint64{"busy": 1, "c1": 2, "c2": 3, "c3": 4}
		for cid, w := range map[string]<-chan sendResult{"busy": busy, "c1": c1, "c2": c2, "c3": c3} {
			got := <-w
			if got.err != nil || got.ack.Seq != want[cid] {
				t.Fatalf("%s: ack %+v err %v, want seq %d", cid, got.ack, got.err, want[cid])
			}
			assertStoredOnce(t, rg, cid, got.ack)
		}
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{2, 3}, []uint64{3, 4})
	})
}

func TestDuplicateFromAnotherWriterFailsAndTheRetryUsesTheReloadedLast(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(func(msgs []domain.Message) []store.Result {
			for seq := uint64(1); seq <= 5; seq++ {
				rg.sub.insert([]domain.Message{{Room: roomA, Seq: seq, From: "mallory", CID: "m"}})
			}
			return rg.sub.insert(msgs)
		})
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		synctest.Wait()
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 6 {
			t.Fatalf("client retry got seq %d, want Last+1 = 6", ack.Seq)
		}
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{6})
		assertStoredOnce(t, rg, "c1", ack)
		if lasts, _, _ := rg.msgs.counts(); lasts != 2 {
			t.Fatalf("Last called %d times, want one load per actor", lasts)
		}
	})
}

func TestDuplicateNotYetVisibleIsResolvedOnceVisible(t *testing.T) {
	tests := map[string]struct {
		writer func(*fakeSubmitter) outcome
		err    error
	}{
		"our earlier write": {func(s *fakeSubmitter) outcome { return s.landedDuplicate }, nil},
		"a foreign write":   {func(s *fakeSubmitter) outcome { return s.foreignFirst }, domain.ErrRetryLater},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rg := newRig(t, baseConfig)
				rg.sub.then(tt.writer(rg.sub))
				rg.msgs.findHook = func(call int) error {
					if call <= 2 {
						return errHidden
					}
					return nil
				}
				rg.start(t)
				begin := time.Now()
				ack, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
				waited := time.Since(begin)
				switch {
				case tt.err != nil:
					expectErr(t, err, tt.err)
				case err != nil:
					t.Fatalf("Send: %v", err)
				default:
					assertStoredOnce(t, rg, "c1", ack)
				}
				assertGroupSeqs(t, rg, []uint64{1})
				if _, _, finds := rg.msgs.counts(); finds != 3 {
					t.Fatalf("Find called %d times, want 3", finds)
				}
				if waited <= 0 || waited >= baseConfig.GroupDeadline {
					t.Fatalf("resolved after %v, want a backoff below the group deadline", waited)
				}
			})
		})
	}
}
