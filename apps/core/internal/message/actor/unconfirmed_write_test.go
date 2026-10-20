package actor_test

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func assertNoAborts(t *testing.T, rg *rig) {
	t.Helper()
	if got := abortedKeys(rg); len(got) != 0 {
		t.Fatalf("aborted %v, want every uncertain cid kept pending", got)
	}
}

func assertFinds(t *testing.T, rg *rig, want int) {
	t.Helper()
	if _, _, finds := rg.msgs.counts(); finds != want {
		t.Fatalf("Find called %d times, want %d", finds, want)
	}
}

func TestUnknownWriteThatIsAbsentFailsAtOnceAndKeepsItsCID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.cids.trackPending()
		rg.sub.then(lostUnknown)
		rg.start(t)
		begin := time.Now()
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if waited := time.Since(begin); waited != 0 {
			t.Fatalf("unconfirmed write answered after %v, want at once", waited)
		}
		assertGroupSeqs(t, rg, []uint64{1})
		assertFinds(t, rg, 1)
		assertNoAborts(t, rg)
		_, err = rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		assertGroupSeqs(t, rg, []uint64{1})
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c2"))
		if ack.Seq != 1 {
			t.Fatalf("next message got seq %d, want 1 since nothing landed", ack.Seq)
		}
	})
}

func TestUnknownWriteThatLandsLaterAnswersTheClientRetryWithTheOriginalSeq(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.cids.trackPending()
		var mu sync.Mutex
		var late []domain.Message
		rg.sub.then(func(msgs []domain.Message) []store.Result {
			mu.Lock()
			late = msgs
			mu.Unlock()
			return lostUnknown(msgs)
		})
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		_, err = rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		mu.Lock()
		rg.sub.insert(late)
		mu.Unlock()
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 1 {
			t.Fatalf("client retry after the write landed got seq %d, want the original 1", ack.Seq)
		}
		assertGroupSeqs(t, rg, []uint64{1})
		assertStoredOnce(t, rg, "c1", ack)
		assertNoAborts(t, rg)
		if next := mustSend(t, rg.Router, cmd(roomA, "alice", "c2")); next.Seq != 2 {
			t.Fatalf("next message got seq %d, want 2 above the landed write", next.Seq)
		}
	})
}

func TestUnconfirmedWriteFailsAfterOneFindAndRetryGetsOriginalAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(rg.sub.landedUnknown)
		rg.msgs.findHook = findTimesOut
		rg.start(t)
		begin := time.Now()
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if waited := time.Since(begin); waited != 0 {
			t.Fatalf("gave up after %v, want at once after one Find", waited)
		}
		if lasts, pages, finds := rg.msgs.counts(); lasts != 1 || pages != 1 || finds != 1 {
			t.Fatalf("after the failure: Last %d, Page %d, Find %d; want 1, 1, 1", lasts, pages, finds)
		}
		assertNoAborts(t, rg)
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 1 {
			t.Fatalf("client retry got seq %d, want the original 1", ack.Seq)
		}
		if n := len(rg.sub.sent()); n != 1 {
			t.Fatalf("client retry submitted a new write (%d groups)", n)
		}
		assertStoredOnce(t, rg, "c1", ack)
		if lasts, pages, _ := rg.msgs.counts(); lasts != 2 || pages != 2 {
			t.Fatalf("dirty actor reloaded Last %d and Page %d times, want 2 and 2", lasts, pages)
		}
		if next := mustSend(t, rg.Router, cmd(roomA, "alice", "c2")); next.Seq != 2 {
			t.Fatalf("next message got seq %d, want 2 above the landed write", next.Seq)
		}
		if lasts, pages, _ := rg.msgs.counts(); lasts != 2 || pages != 2 {
			t.Fatalf("clean actor reloaded again: Last %d, Page %d", lasts, pages)
		}
	})
}

func TestDuplicateNotYetVisibleFailsAfterOneFind(t *testing.T) {
	tests := map[string]struct {
		writer   func(*fakeSubmitter) outcome
		original bool
	}{
		"our earlier write": {func(s *fakeSubmitter) outcome { return s.landedDuplicate }, true},
		"a foreign write":   {func(s *fakeSubmitter) outcome { return s.foreignFirst }, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rg := newRig(t, baseConfig)
				rg.sub.then(tt.writer(rg.sub))
				rg.msgs.findHook = func(call int) error {
					if call == 1 {
						return errHidden
					}
					return nil
				}
				rg.start(t)
				begin := time.Now()
				_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
				expectErr(t, err, domain.ErrRetryLater)
				if waited := time.Since(begin); waited != 0 {
					t.Fatalf("unconfirmed duplicate answered after %v, want at once", waited)
				}
				assertGroupSeqs(t, rg, []uint64{1})
				assertFinds(t, rg, 1)
				assertNoAborts(t, rg)
				if !tt.original {
					return
				}
				ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
				if ack.Seq != 1 {
					t.Fatalf("client retry got seq %d, want the original 1", ack.Seq)
				}
				assertGroupSeqs(t, rg, []uint64{1})
				assertStoredOnce(t, rg, "c1", ack)
			})
		})
	}
}
