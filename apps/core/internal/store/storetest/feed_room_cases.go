package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func feedRooms(t *testing.T, msgs store.Messages, rooms store.Rooms, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	first, firstMembers := teamOf(roomA)
	second, secondMembers := teamOf(roomB)
	mustCreate(t, rooms, first, firstMembers)
	insertEach(t, msgs, msg(roomA, mainThread, 1))
	mustCreate(t, rooms, second, secondMembers)
	insertEach(t, msgs, msg(roomB, mainThread, 1))
	got := nextChanges(t, cur, 8)
	kinds := []store.ChangeKind{
		store.RoomInserted, store.MemberChanged, store.MemberChanged, store.MessageInserted,
		store.RoomInserted, store.MemberChanged, store.MemberChanged, store.MessageInserted,
	}
	for i, c := range got {
		if c.Kind != kinds[i] {
			t.Fatalf("change %d kind = %d, want %d", i, c.Kind, kinds[i])
		}
	}
	assertChangedRoom(t, got[0], first)
	assertChangedRoom(t, got[4], second)
	for i, c := range []store.Change{got[1], got[2], got[5], got[6]} {
		if want := []string{"alice", "bob"}[i%2]; c.Member.User != want || c.Member.Ver != 1 || c.Room != (domain.Room{}) {
			t.Fatalf("creation member change %d = %+v, want %q at ver 1", i, c, want)
		}
	}
	msgChanges := []store.Change{got[3], got[7]}
	assertMessages(t, messagesOf(msgChanges), []domain.Message{msg(roomA, mainThread, 1), msg(roomB, mainThread, 1)})
	for _, c := range msgChanges {
		if c.Room != (domain.Room{}) {
			t.Fatalf("message change carries room %+v, want none", c.Room)
		}
	}
}

func assertChangedRoom(t *testing.T, c store.Change, want domain.Room) {
	t.Helper()
	got := c.Room
	gotAt, wantAt := got.CreatedAt, want.CreatedAt
	got.CreatedAt, want.CreatedAt = time.Time{}, time.Time{}
	if got != want || !gotAt.Equal(wantAt) || c.Msg.Seq != 0 {
		t.Fatalf("room change = %+v at %v with message %+v, want %+v at %v and no message", got, gotAt, c.Msg, want, wantAt)
	}
}
