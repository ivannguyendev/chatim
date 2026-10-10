package actor_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
)

func TestUnconfirmedWriteFailsAtDeadlineAndRetryGetsOriginalAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(rg.sub.landedUnknown)
		rg.msgs.findHook = func(int) error { return errors.New("find timed out") }
		rg.start(t)

		begin := time.Now()
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if waited := time.Since(begin); waited != baseConfig.GroupDeadline {
			t.Fatalf("gave up after %v, want the group deadline %v", waited, baseConfig.GroupDeadline)
		}
		if lasts, pages, finds := rg.msgs.counts(); lasts != 1 || pages != 1 || finds < 2 {
			t.Fatalf("after deadline: Last %d, Page %d, Find %d; want 1, 1, retried", lasts, pages, finds)
		}

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

		next := mustSend(t, rg.Router, cmd(roomA, "alice", "c2"))
		if next.Seq != 2 {
			t.Fatalf("next message got seq %d, want 2 above the landed write", next.Seq)
		}
		if lasts, pages, _ := rg.msgs.counts(); lasts != 2 || pages != 2 {
			t.Fatalf("clean actor reloaded again: Last %d, Page %d", lasts, pages)
		}
	})
}

func TestUnconfirmedRetryAtSameSeqLimitMarksActorDirty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.alwaysDo(lostUnknown)
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{1}, []uint64{1}, []uint64{1})
		rg.sub.alwaysDo(nil)
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c2"))
		if ack.Seq != 1 {
			t.Fatalf("next message got seq %d, want 1 since nothing landed", ack.Seq)
		}
		if lasts, pages, _ := rg.msgs.counts(); lasts != 2 || pages != 2 {
			t.Fatalf("actor did not reload after an unconfirmed failure: Last %d, Page %d", lasts, pages)
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

func TestNotSentAndResendBudgetsAreSeparate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(notSent, notSent, notSent, lostUnknown, lostUnknown, lostUnknown)
		rg.start(t)
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 1 {
			t.Fatalf("ack seq %d, want 1 after three unsent tries and three resends", ack.Seq)
		}
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{1}, []uint64{1}, []uint64{1}, []uint64{1}, []uint64{1}, []uint64{1})
		assertStoredOnce(t, rg, "c1", ack)
	})
}

func TestNotSentLimitFailsAndReleasesWithoutYielding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.alwaysDo(notSent)
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{1}, []uint64{1}, []uint64{1})
		if got, want := abortedKeys(rg), []dedupe.Key{remoteKey(roomA, "alice", "c1")}; !slices.Equal(got, want) {
			t.Fatalf("aborted %v, want %v", got, want)
		}
		synctest.Wait()
		if n, yields := rg.ActorCount(), rg.Stats().Yields; n != 1 || yields != 0 {
			t.Fatalf("actors %d yields %d after unsent writes, want the actor kept and no yield", n, yields)
		}
	})
}
