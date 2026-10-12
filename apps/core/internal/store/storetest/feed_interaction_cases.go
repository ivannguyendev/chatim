package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type interactionChange struct {
	kind              store.ChangeKind
	room, thread, seq uint64
	user              string
	ver               uint32
}

func RunInteractionFeed(t *testing.T, open func(t *testing.T) (store.Interactions, store.ChangeFeed)) {
	t.Helper()
	t.Run("reaction and bookmark changes come out in commit order and no-ops and replies add none", func(t *testing.T) {
		in, feed := open(t)
		cur := openCursor(t, feed)
		key := msgKey(roomA, mainThread, 1)
		mustSet(t, in, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
		mustSet(t, in, reactionOf(roomA, mainThread, 1, "alice", "👍"), false)
		mustSetBookmark(t, in, bookmarkAt(roomA, mainThread, 1, "alice", true, 0), true)
		mustSetBookmark(t, in, bookmarkAt(roomA, mainThread, 1, "alice", true, time.Second), false)
		mustAddReply(t, in, replyAt(roomA, 1, 2, "alice", 0), true)
		mustRemoveReply(t, in, replyAt(roomA, 1, 2, "alice", 0), true)
		mustSetBookmark(t, in, bookmarkAt(roomA, mainThread, 1, "alice", false, time.Second), true)
		mustSet(t, in, reactAt(roomA, mainThread, 1, "alice", "❤️", time.Second), true)
		mustRemove(t, in, key, "alice", baseTime.Add(2*time.Second), true)
		mustRemove(t, in, key, "alice", baseTime.Add(3*time.Second), false)
		mustRemove(t, in, key, "bob", baseTime, false)
		mustSetBookmark(t, in, bookmarkAt(roomB, sideThread, 2, "bob", true, 0), true)
		mustSet(t, in, reactionOf(roomB, sideThread, 2, "bob", "😂"), true)
		want := []interactionChange{
			{store.ReactionChanged, roomA, mainThread, 1, "alice", 1},
			{store.BookmarkChanged, roomA, mainThread, 1, "alice", 1},
			{store.BookmarkChanged, roomA, mainThread, 1, "alice", 2},
			{store.ReactionChanged, roomA, mainThread, 1, "alice", 2},
			{store.ReactionChanged, roomA, mainThread, 1, "alice", 3},
			{store.BookmarkChanged, roomB, sideThread, 2, "bob", 1},
			{store.ReactionChanged, roomB, sideThread, 2, "bob", 1},
		}
		for i, c := range nextChanges(t, cur, len(want)) {
			if got := interactionOf(c); got != want[i] || c.Msg.Seq != 0 || c.Room.ID != 0 || c.Edit.Version != 0 || c.Pin.PV != 0 {
				t.Fatalf("change %d = %+v from %+v, want %+v", i, got, c, want[i])
			}
		}
	})
}

func interactionOf(c store.Change) interactionChange {
	if c.Kind == store.BookmarkChanged {
		b := c.Bookmark
		return interactionChange{c.Kind, b.Room, b.Thread, b.Seq, b.User, b.Ver}
	}
	r := c.Reaction
	return interactionChange{c.Kind, r.Room, r.Thread, r.Seq, r.User, r.N}
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
