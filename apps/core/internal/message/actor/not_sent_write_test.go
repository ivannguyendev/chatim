package actor_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestNotSentWriteFailsAtOnceAndReleasesItsCID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(notSent)
		rg.start(t)
		begin := time.Now()
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if waited := time.Since(begin); waited != 0 {
			t.Fatalf("unsent write answered after %v, want at once", waited)
		}
		assertGroupSeqs(t, rg, []uint64{1})
		if _, _, finds := rg.msgs.counts(); finds != 0 {
			t.Fatalf("a write that was never sent was looked up %d times", finds)
		}
		if got, want := abortedKeys(rg), []dedupe.Key{remoteKey(roomA, "alice", "c1")}; !slices.Equal(got, want) {
			t.Fatalf("aborted %v, want %v", got, want)
		}
		synctest.Wait()
		if n, yields := rg.ActorCount(), rg.Stats().Yields; n != 1 || yields != 0 {
			t.Fatalf("actors %d yields %d after an unsent write, want the actor kept and no yield", n, yields)
		}
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 1 {
			t.Fatalf("client retry got seq %d, want 1 since nothing was written", ack.Seq)
		}
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{1})
		assertStoredOnce(t, rg, "c1", ack)
		if lasts, _, _ := rg.msgs.counts(); lasts != 1 {
			t.Fatalf("a definite failure reloaded Last (%d calls)", lasts)
		}
	})
}

func TestGroupThatExpiresBeforeSubmitIsRetryLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.cids.delay = baseConfig.GroupDeadline + time.Millisecond
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "x"))
		expectErr(t, err, domain.ErrRetryLater)
		if n := len(rg.sub.sent()); n != 0 {
			t.Fatalf("expired group submitted %d times", n)
		}
		want := [][]dedupe.Key{{remoteKey(roomA, "alice", "x")}}
		if _, _, aborts := rg.cids.calls(); !slices.EqualFunc(aborts, want, slices.Equal) {
			t.Fatalf("aborts = %v, want %v for a group that was never sent", aborts, want)
		}
	})
}

func TestSubmitFailureWritesNothingAndReusesTheSeq(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.err = domain.ErrBusy
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrBusy)
		rg.sub.mu.Lock()
		rg.sub.err = nil
		rg.sub.mu.Unlock()
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c2"))
		if ack.Seq != 1 {
			t.Fatalf("seq after a refused submit = %d, want 1", ack.Seq)
		}
		if lasts, _, _ := rg.msgs.counts(); lasts != 1 {
			t.Fatalf("clean submit failure reloaded Last (%d calls)", lasts)
		}
	})
}
