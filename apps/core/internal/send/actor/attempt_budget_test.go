package actor_test

import (
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestNotSentWriteIsRetriedWithoutFind(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(notSent)
		rg.start(t)
		begin := time.Now()
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{1})
		assertStoredOnce(t, rg, "c1", ack)
		if _, _, finds := rg.msgs.counts(); finds != 0 {
			t.Fatalf("a write that was never sent was looked up %d times", finds)
		}
		want := begin.Add(baseConfig.GroupDeadline)
		if dl := rg.sub.groupDeadlines(); len(dl) != 2 || !dl[0].Equal(want) || !dl[1].Equal(want) {
			t.Fatalf("group deadlines = %v, want both %v", dl, want)
		}
	})
}

func TestNotSentResendIsStillReconciled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(lostUnknown, notSent)
		rg.start(t)
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{1}, []uint64{1})
		assertStoredOnce(t, rg, "c1", ack)
		if _, _, finds := rg.msgs.counts(); finds != 2 {
			t.Fatalf("Find called %d times, want 2 since the first attempt stayed ambiguous", finds)
		}
	})
}

func TestEntryIsAbandonedBeforeItsReservationExpires(t *testing.T) {
	tests := map[string]struct {
		write   outcome
		release bool
	}{
		"ambiguous writes": {lostUnknown, false},
		"writes not sent":  {notSent, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := baseConfig
				cfg.ReservationTTL = 4 * time.Second
				rg := newRig(t, cfg)
				var mu sync.Mutex
				var attempts []time.Time
				rg.sub.alwaysDo(func(msgs []domain.Message) []store.Result {
					mu.Lock()
					attempts = append(attempts, time.Now())
					mu.Unlock()
					time.Sleep(900 * time.Millisecond)
					return tt.write(msgs)
				})
				rg.start(t)
				begin := time.Now()
				_, err := rg.Send(t.Context(), cmd(roomA, "alice", "x"))
				expectErr(t, err, domain.ErrRetryLater)
				budgetEnd := begin.Add(cfg.ReservationTTL - time.Second)
				if waited := time.Since(begin); waited > cfg.ReservationTTL-time.Second {
					t.Fatalf("abandoned after %v, want before %v", waited, cfg.ReservationTTL-time.Second)
				}
				time.Sleep(cfg.ReservationTTL)
				synctest.Wait()
				mu.Lock()
				defer mu.Unlock()
				if len(attempts) != 3 {
					t.Fatalf("made %d attempts, want 3: the time budget must stop it before the count limit of 4", len(attempts))
				}
				for i, at := range attempts {
					if at.Add(cfg.GroupDeadline).After(budgetEnd) {
						t.Fatalf("attempt %d at %v could run past %v", i, at.Sub(begin), budgetEnd.Sub(begin))
					}
				}
				var want [][]dedupe.Key
				if tt.release {
					want = [][]dedupe.Key{{remoteKey(roomA, "alice", "x")}}
				}
				if _, _, aborts := rg.cids.calls(); !slices.EqualFunc(aborts, want, slices.Equal) {
					t.Fatalf("aborts = %v, want %v", aborts, want)
				}
				if docs := storedCIDs(t, rg.msgs.Messages, roomA)["x"]; len(docs) != 0 {
					t.Fatalf("abandoned write stored %d times", len(docs))
				}
			})
		})
	}
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
