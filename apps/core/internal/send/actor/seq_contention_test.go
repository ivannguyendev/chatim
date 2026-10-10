package actor_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func assertYielded(t *testing.T, rg *rig) {
	t.Helper()
	synctest.Wait()
	if n := rg.ActorCount(); n != 0 {
		t.Fatalf("actors after contention = %d, want 0", n)
	}
	if got := rg.Stats().Yields; got != 1 {
		t.Fatalf("yields = %d, want 1", got)
	}
}

func abortedKeys(rg *rig) []dedupe.Key {
	_, _, aborts := rg.cids.calls()
	return slices.Concat(aborts...)
}

func TestSeqContentionFailsAtOnceAndYieldsTheRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.alwaysDo(rg.sub.foreignFirst)
		rg.start(t)
		begin := time.Now()
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if waited := time.Since(begin); waited != 0 {
			t.Fatalf("contention answered after %v, want at once", waited)
		}
		assertGroupSeqs(t, rg, []uint64{1})
		assertYielded(t, rg)
		if got, want := abortedKeys(rg), []dedupe.Key{remoteKey(roomA, "alice", "c1")}; !slices.Equal(got, want) {
			t.Fatalf("aborted %v, want %v", got, want)
		}
		if docs := storedCIDs(t, rg.msgs.Messages, roomA)["c1"]; len(docs) != 0 {
			t.Fatalf("contended message stored %d times", len(docs))
		}
	})
}

func TestSeqContentionFailsTheRestOfTheGroupAndKeepsCommittedAcks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		mixed := func(msgs []domain.Message) []store.Result {
			rg.sub.insert([]domain.Message{{Room: roomA, Seq: msgs[1].Seq, From: "mallory", CID: "m"}})
			ins := rg.sub.insert([]domain.Message{msgs[0], msgs[1], msgs[3]})
			return []store.Result{ins[0], ins[1], notSent(msgs[2:3])[0], ins[2], lostUnknown(msgs[4:])[0]}
		}
		rg.sub.then(rg.sub.insert, mixed)
		rg.start(t)
		ctx := t.Context()
		busy := sendAsync(ctx, rg.Router, cmd(roomA, "bob", "busy"))
		synctest.Wait()
		waits := map[string]<-chan sendResult{}
		for _, cid := range []string{"c1", "c2", "c3", "c4", "c5"} {
			waits[cid] = sendAsync(ctx, rg.Router, cmd(roomA, "alice", cid))
			synctest.Wait()
		}
		rg.sub.release()
		synctest.Wait()
		rg.sub.release()
		if got := <-busy; got.err != nil {
			t.Fatalf("busy: %v", got.err)
		}
		for cid, seq := range map[string]uint64{"c1": 2, "c4": 5} {
			got := <-waits[cid]
			if got.err != nil || got.ack.Seq != seq {
				t.Fatalf("%s: ack %+v err %v, want seq %d", cid, got.ack, got.err, seq)
			}
			assertStoredOnce(t, rg, cid, got.ack)
		}
		for _, cid := range []string{"c2", "c3", "c5"} {
			expectErr(t, (<-waits[cid]).err, domain.ErrRetryLater)
		}
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{2, 3, 4, 5, 6})
		assertYielded(t, rg)
		want := []dedupe.Key{remoteKey(roomA, "alice", "c2"), remoteKey(roomA, "alice", "c3")}
		if got := abortedKeys(rg); !slices.Equal(got, want) {
			t.Fatalf("aborted %v, want %v: the ambiguous c5 keeps its reservation", got, want)
		}
	})
}

func TestCIDPendingOnAnotherCoreIsCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.cids.force(dedupe.Key{Room: roomA, User: "alice", CID: "c1"}, dedupe.PendingElsewhere, dedupe.Record{})
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if got := rg.Stats().CIDElsewhere; got != 1 {
			t.Fatalf("cid pending elsewhere = %d, want 1", got)
		}
	})
}
