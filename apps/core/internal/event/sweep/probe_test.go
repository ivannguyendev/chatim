package sweep_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type countingStates struct {
	store.EventStates
	mu     sync.Mutex
	probes map[store.StateCollection]int
	pages  map[store.StateCollection]int
}

func (c *countingStates) Pending(ctx context.Context, coll store.StateCollection, from, before time.Time, after store.PendingCursor, limit int) ([]store.PendingDoc, store.PendingCursor, error) {
	c.mu.Lock()
	if limit == 1 {
		c.probes[coll]++
	} else {
		c.pages[coll]++
	}
	c.mu.Unlock()
	return c.EventStates.Pending(ctx, coll, from, before, after, limit)
}

func (c *countingStates) counts(coll store.StateCollection) (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.probes[coll], c.pages[coll]
}

func TestVersionMovedMidSweepWithAFailedPublishIsRepublishedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var bumped atomic.Bool
		var rg *rig
		rg = newRig(t, setup, func(r sweep.Rebuilder) sweep.Rebuilder {
			return rebuildFunc(func(ctx context.Context, d store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
				evs, err := r.Rebuild(ctx, d, bit)
				if bit == store.BitReactionCounts && bumped.CompareAndSwap(false, true) {
					_, _ = rg.msgs.AddReactionCounts(ctx, store.MsgKey{Room: d.Room, Seq: d.Seq}, []store.EmojiDelta{{Emoji: "👍", Delta: 1}})
				}
				return evs, err
			})
		})
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		rg.send(t, room, 1, old)
		if _, err := rg.msgs.AddReactionCounts(t.Context(), store.MsgKey{Room: room, Seq: 1}, []store.EmojiDelta{{Emoji: "👍", Delta: 1}}); err != nil {
			t.Fatalf("AddReactionCounts: %v", err)
		}
		rg.start()
		expectPending(t, rg, store.StateMessages, 1)
		expectCheckpoint(t, rg, store.StateMessages, time.Now().Add(-minAge))
		if got := rg.PendingOldest(store.StateMessages); got != time.Minute {
			t.Fatalf("PendingOldest = %v, want the stuck doc's 1m", got)
		}
		if n := rg.sink.Count(staleMsg); n != 1 {
			t.Fatalf("%q logged %d times, want 1", staleMsg, n)
		}
		rg.nextPass()
		expectPending(t, rg, store.StateMessages, 0)
		expectIDs(t, rg, pbconv.RoomCreatedEventID(room), pbconv.MessageEventID(room, 0, 1),
			pbconv.ReactionCountsEventID(room, 0, 1, 1), pbconv.ReactionCountsEventID(room, 0, 1, 2))
		expectCheckpoint(t, rg, store.StateMessages, time.Now().Add(-minAge))
		rg.stop(t)
	})
}

func TestProbeShowsADocBeforeTheWindowInTheGaugeAndSweepsItNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		ancient := time.Now().Add(-10 * time.Minute)
		rg.createRoom(t, room, ancient)
		rg.start()
		rg.send(t, room, 1, ancient)
		rg.nextPass()
		expectPending(t, rg, store.StateMessages, 1)
		if got, want := rg.PendingOldest(store.StateMessages), time.Since(ancient); got != want {
			t.Fatalf("PendingOldest = %v, want %v", got, want)
		}
		rg.nextPass()
		expectPending(t, rg, store.StateMessages, 0)
		expectIDs(t, rg, pbconv.RoomCreatedEventID(room), pbconv.MessageEventID(room, 0, 1))
		rg.nextPass()
		if got := rg.PendingOldest(store.StateMessages); got != 0 {
			t.Fatalf("PendingOldest after the sweep = %v, want 0", got)
		}
		rg.stop(t)
	})
}

func TestProbeCostsOnePendingCallPerCollectionPerPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		counting := &countingStates{EventStates: rg.states, probes: map[store.StateCollection]int{}, pages: map[store.StateCollection]int{}}
		rg.replaceStates(t, counting)
		rg.start()
		rg.nextPass()
		rg.nextPass()
		if rg.Passes() != 3 {
			t.Fatalf("passes = %d, want 3", rg.Passes())
		}
		for _, c := range []store.StateCollection{store.StateMessages, store.StateRooms} {
			if probes, pages := counting.counts(c); probes != 3 || pages != 3 {
				t.Fatalf("%s: %d probes and %d page reads in 3 passes, want 3 and 3", c, probes, pages)
			}
		}
		if probes, pages := counting.counts(store.StateMembers); probes+pages != 0 {
			t.Fatalf("members without a rebuilder read %d times, want 0", probes+pages)
		}
		rg.stop(t)
	})
}
