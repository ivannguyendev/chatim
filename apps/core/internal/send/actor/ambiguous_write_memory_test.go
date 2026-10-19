package actor_test

import (
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type lateWrite struct {
	mu   sync.Mutex
	msgs []domain.Message
}

func (l *lateWrite) lose(msgs []domain.Message) []store.Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = msgs
	return lostUnknown(msgs)
}

func (l *lateWrite) land(rg *rig) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rg.sub.insert(l.msgs)
}

func TestLateUncertainWriteAnswersTheRetryWithItsOriginalSeqAfterOtherTrafficReloadedTheRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		late := &lateWrite{}
		rg.sub.then(late.lose)
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		_, err = rg.Send(t.Context(), cmd(roomA, "mallory", "m1"))
		expectErr(t, err, domain.ErrNotMember)
		if lasts, _, _ := rg.msgs.counts(); lasts != 2 {
			t.Fatalf("Last called %d times, want the other send to reload the room before the write lands", lasts)
		}
		late.land(rg)
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 1 {
			t.Fatalf("client retry got seq %d, want the original 1", ack.Seq)
		}
		if n := len(rg.sub.sent()); n != 1 {
			t.Fatalf("client retry submitted a new write (%d groups)", n)
		}
		assertStoredOnce(t, rg, "c1", ack)
		assertFinds(t, rg, 2)
		_, commits, _ := rg.cids.calls()
		if len(commits) != 1 || commits[0][0].Key != remoteKey(roomA, "alice", "c1") || commits[0][0].Record.Seq != 1 {
			t.Fatalf("commits = %v, want the late write committed at seq 1", commits)
		}
		if next := mustSend(t, rg.Router, cmd(roomA, "bob", "b1")); next.Seq != 2 {
			t.Fatalf("next message got seq %d, want 2 above the late write", next.Seq)
		}
	})
}

func TestAbsentUncertainWriteStaysRememberedAndAnswersRetryLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(lostUnknown)
		rg.start(t)
		for range 3 {
			_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
			expectErr(t, err, domain.ErrRetryLater)
		}
		assertGroupSeqs(t, rg, []uint64{1})
		assertFinds(t, rg, 3)
		assertNoAborts(t, rg)
	})
}

func TestUncertainWriteIsForgottenWhenItsReservationEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(lostUnknown)
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		time.Sleep(baseConfig.ReservationTTL)
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 1 {
			t.Fatalf("retry after the reservation got seq %d, want 1 since nothing landed", ack.Seq)
		}
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{1})
		assertFinds(t, rg, 1)
	})
}

func TestUncertainWriteWhoseSeqWentToAnotherWriterIsReleased(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(lostUnknown)
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		rg.sub.insert([]domain.Message{{Room: roomA, Seq: 1, From: "mallory", CID: "m"}})
		_, err = rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if got, want := abortedKeys(rg), []dedupe.Key{remoteKey(roomA, "alice", "c1")}; !slices.Equal(got, want) {
			t.Fatalf("aborted %v, want %v: a write whose seq is taken can never land", got, want)
		}
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 2 {
			t.Fatalf("retry got seq %d, want 2 above the other writer", ack.Seq)
		}
		assertStoredOnce(t, rg, "c1", ack)
	})
}

func TestSettleFindHasItsOwnBudgetAfterTheGroupDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(func(msgs []domain.Message) []store.Result {
			time.Sleep(baseConfig.GroupDeadline + time.Millisecond)
			return rg.sub.landedUnknown(msgs)
		})
		rg.start(t)
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 1 {
			t.Fatalf("ack seq %d, want 1 confirmed by the Find after the group deadline", ack.Seq)
		}
		assertFinds(t, rg, 1)
		assertStoredOnce(t, rg, "c1", ack)
	})
}
