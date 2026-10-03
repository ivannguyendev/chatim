package actor_test

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestEvictSlotsRetiresOnlyActorsOfThoseSlots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		sibling := roomSharingSlotWith(roomA)
		createRoom(t, rg.rooms, sibling, "alice", "bob")
		rg.start(t)
		for _, room := range []uint64{roomA, sibling, roomB} {
			mustSend(t, rg.Router, cmd(room, "alice", "c1"))
		}
		rg.EvictSlots(t.Context(), slotsOf(roomA))
		if n := rg.ActorCount(); n != 1 {
			t.Fatalf("%d actors after evicting slot %d, want only room %d's left", n, slotmap.Of(roomA), roomB)
		}
		loads, _, _ := rg.msgs.counts()
		if ack := mustSend(t, rg.Router, cmd(roomB, "alice", "c2")); ack.Seq != 2 {
			t.Fatalf("untouched room %d assigned seq %d, want 2", roomB, ack.Seq)
		}
		if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c2")); ack.Seq != 2 {
			t.Fatalf("fresh actor of room %d assigned seq %d, want 2", roomA, ack.Seq)
		}
		if after, _, _ := rg.msgs.counts(); after != loads+1 {
			t.Fatalf("Last called %d more times, want one reload for the retired room only", after-loads)
		}
	})
}

func TestRetireSettlesTheInFlightGroupAndTurnsAwayTheRest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		ctx := t.Context()
		inFlight := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1"))
		synctest.Wait()
		queued := []<-chan sendResult{sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c2")), sendAsync(ctx, rg.Router, cmd(roomA, "bob", "c3"))}
		synctest.Wait()
		evicted := evictAsync(ctx, rg.Router, roomA)
		synctest.Wait()
		select {
		case <-evicted:
			t.Fatal("EvictSlots returned while a group was in flight")
		default:
		}
		_, err := rg.Send(ctx, cmd(roomA, "alice", "late"))
		expectErr(t, err, domain.ErrRetryLater)

		rg.sub.release()
		if got := <-inFlight; got.err != nil || got.ack.Seq != 1 {
			t.Fatalf("in-flight command got %+v, %v; want acked at seq 1", got.ack, got.err)
		}
		for _, w := range queued {
			expectErr(t, (<-w).err, domain.ErrRetryLater)
		}
		<-evicted
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("%d actors after retire, want 0", n)
		}
		if evs := rg.events.events(roomA); len(evs) != 1 || evs[0].GetSeq() != 1 {
			t.Fatalf("published %d events, want only seq 1 of the settled group", len(evs))
		}
		reserves, commits, aborts := rg.cids.calls()
		if len(reserves) != 1 || len(commits) != 1 || len(commits[0]) != 1 || commits[0][0].Key != remoteKey(roomA, "alice", "c1") || len(aborts) != 0 {
			t.Fatalf("reserves %v commits %v aborts %v, want only c1 reserved and committed", reserves, commits, aborts)
		}
		rg.sub.open()
		if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c2")); ack.Seq != 2 {
			t.Fatalf("turned-away command resent to a fresh actor got seq %d, want 2", ack.Seq)
		}
	})
}

func TestRetireFailsPendingRetriesWithTheirCertainty(t *testing.T) {
	tests := map[string]struct {
		write   func(*rig) outcome
		release bool
	}{
		"ambiguous write keeps the reservation": {func(*rig) outcome { return lostUnknown }, false},
		"unsent write releases it":              {func(*rig) outcome { return notSent }, true},
		"taken seq releases it":                 {func(rg *rig) outcome { return rg.sub.foreignFirst }, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rg := newRig(t, baseConfig)
				rg.sub.then(tt.write(rg))
				rg.sub.hold()
				rg.start(t)
				w := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "x"))
				synctest.Wait()
				evicted := evictAsync(t.Context(), rg.Router, roomA)
				synctest.Wait()
				rg.sub.release()
				expectErr(t, (<-w).err, domain.ErrRetryLater)
				<-evicted
				if n := len(rg.sub.sent()); n != 1 {
					t.Fatalf("submitted %d groups, want the retry dropped by retire", n)
				}
				var want [][]dedupe.Key
				if tt.release {
					want = [][]dedupe.Key{{remoteKey(roomA, "alice", "x")}}
				}
				if _, commits, aborts := rg.cids.calls(); !slices.EqualFunc(aborts, want, slices.Equal) || len(commits) != 0 {
					t.Fatalf("aborts %v commits %v, want aborts %v and no commits", aborts, commits, want)
				}
			})
		})
	}
}

func TestEvictSlotsReturnsAtItsDeadlineWhileAGroupIsStuck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		ctx := t.Context()
		w := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1"))
		synctest.Wait()
		const wait = 100 * time.Millisecond
		ectx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		begin := time.Now()
		rg.EvictSlots(ectx, slotsOf(roomA))
		rg.EvictSlots(ectx, slotsOf(roomA))
		if waited := time.Since(begin); waited != wait {
			t.Fatalf("EvictSlots waited %v, want its %v deadline", waited, wait)
		}
		if n := rg.ActorCount(); n != 1 {
			t.Fatalf("%d actors, want the stuck one still retiring", n)
		}
		_, err := rg.Send(ctx, cmd(roomA, "bob", "c2"))
		expectErr(t, err, domain.ErrRetryLater)

		rg.sub.open()
		if got := <-w; got.err != nil {
			t.Fatalf("stuck group failed after EvictSlots gave up: %v", got.err)
		}
		synctest.Wait()
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("%d actors once the stuck group settled, want 0", n)
		}
	})
}

func TestFreshActorAfterRetireReloadsSeqWrittenByAnotherCore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		rg.EvictSlots(t.Context(), slotsOf(roomA))
		other, _ := runRouter(t, rg.msgs.Messages, rg.rooms, &fakeSubmitter{store: rg.msgs.Messages}, &fakeRegistry{}, baseConfig)
		mustSend(t, other, cmd(roomA, "bob", "c2"))
		before := len(rg.sub.sent())
		if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c3")); ack.Seq != 3 {
			t.Fatalf("fresh actor assigned seq %d, want 3", ack.Seq)
		}
		if groups := rg.sub.sent()[before:]; len(groups) != 1 || groups[0][0].Seq != 3 {
			t.Fatalf("groups after retire %v, want one first-try write at seq 3 from the reloaded Last", groups)
		}
	})
}
