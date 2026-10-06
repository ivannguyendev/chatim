package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func RunReactionFeed(t *testing.T, open func(t *testing.T) (store.Reactions, store.ChangeFeed)) {
	t.Helper()
	t.Run("reaction changes come out in commit order and no-ops add none", func(t *testing.T) {
		reactions, feed := open(t)
		cur := openCursor(t, feed)
		key := msgKey(roomA, mainThread, 1)
		first := mustSet(t, reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
		mustSet(t, reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), false)
		second := mustSet(t, reactions, reactAt(roomA, mainThread, 1, "alice", "❤️", time.Second), true)
		removed := mustRemove(t, reactions, key, "alice", baseTime.Add(2*time.Second), true)
		mustRemove(t, reactions, key, "alice", baseTime.Add(3*time.Second), false)
		mustRemove(t, reactions, key, "bob", baseTime, false)
		other := mustSet(t, reactions, reactionOf(roomB, sideThread, 2, "bob", "😂"), true)
		want := []domain.Reaction{first, second, removed, other}
		for i, c := range nextChanges(t, cur, len(want)) {
			got, w := c.Reaction, want[i]
			if c.Kind != store.ReactionChanged || c.Msg.Seq != 0 || c.Room.ID != 0 || c.Edit.Version != 0 || c.Pin.PV != 0 {
				t.Fatalf("change %d = %+v, want only a reaction change", i, c)
			}
			if got.Room != w.Room || got.Thread != w.Thread || got.Seq != w.Seq || got.User != w.User || got.N != w.N {
				t.Fatalf("change %d = %d/%d/%d %q n%d, want %d/%d/%d %q n%d", i, got.Room, got.Thread, got.Seq, got.User, got.N, w.Room, w.Thread, w.Seq, w.User, w.N)
			}
		}
	})
}

func RunPinFeed(t *testing.T, open func(t *testing.T) (store.Pins, store.ChangeFeed)) {
	t.Helper()
	t.Run("pin facts come out in commit order with their content and a refused append adds none", func(t *testing.T) {
		pins, feed := open(t)
		cur := openCursor(t, feed)
		first, second := pinFact(roomA, 1, domain.PinOpPin, 3), pinFact(roomA, 2, domain.PinOpUnpin, 3)
		other := pinFact(roomB, 1, domain.PinOpPin, 9)
		mustAppendPins(t, pins, first)
		assertErrorIs(t, "Append(existing version)", pins.Append(t.Context(), pinFact(roomA, 1, domain.PinOpPin, 8)), store.ErrPinExists)
		mustAppendPins(t, pins, second, other)
		got := nextChanges(t, cur, 3)
		facts := make([]domain.PinAction, len(got))
		for i, c := range got {
			if c.Kind != store.PinInserted || c.Msg.Seq != 0 || c.Room.ID != 0 || c.Reaction.N != 0 {
				t.Fatalf("change %d = %+v, want only a pin fact", i, c)
			}
			facts[i] = c.Pin
		}
		assertPinActions(t, "pin changes", facts, []domain.PinAction{first, second, other})
	})
}
