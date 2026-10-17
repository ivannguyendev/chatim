package roomlist_test

import (
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomlist"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const manyBase uint64 = 50_000

func seedMany(t *testing.T, n int) *countingStore {
	t.Helper()
	c := newWorld()
	acts := make([]store.Activity, 0, n)
	for i := range n {
		room := manyBase + uint64(i)
		me := person(room, acme, minh, domain.RoleMember, uint64(i%3))
		me.JoinedAt = day.Add(-time.Duration(i%97) * time.Hour)
		if i%11 == 0 {
			me.Preferences.PinnedAt = day.Add(time.Duration(i%5) * time.Minute)
		}
		if i%7 == 0 {
			me.Preferences.ArchivedAt = day
		}
		c.create(t, group(room, acme, fmt.Sprintf("Room %d", i)), person(room, acme, lan, domain.RoleOwner, 0), me)
		if i%5 != 0 {
			acts = append(acts, store.Activity{Room: room, Seq: uint64(i%13 + 1), At: day.Add(-time.Duration(i%89) * time.Minute)})
		}
	}
	if err := c.rooms.TouchActivity(t.Context(), acts); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	return c
}

type itemShape struct {
	room    uint64
	readSeq uint64
	pinned  time.Time
}

func walkAll(t *testing.T, l *roomlist.Lister, folder domain.RoomFolder) []itemShape {
	t.Helper()
	var out []itemShape
	var after *domain.RoomSortKey
	for range 100 {
		p := list(t, l, acme, folder, after, roomlist.MaxRooms)
		for _, it := range p.Items {
			out = append(out, itemShape{room: it.Room.ID, readSeq: it.Me.ReadSeq, pinned: it.Me.Preferences.PinnedAt})
		}
		if p.Next == nil {
			return out
		}
		after = p.Next
	}
	t.Fatal("pagination did not end")
	return nil
}

func TestTheJoinLimitPicksThePathAndBothPathsListTheSameRooms(t *testing.T) {
	for _, tc := range []struct {
		rooms   int
		path    string
		another string
	}{
		{rooms: 2000, path: "JoinedRoomOrder", another: "StepwiseRoomOrder"},
		{rooms: 2001, path: "StepwiseRoomOrder", another: "JoinedRoomOrder"},
	} {
		t.Run(strconv.Itoa(tc.rooms), func(t *testing.T) {
			st := seedMany(t, tc.rooms)
			chosen := newLister(t, st, 2000)
			list(t, chosen, acme, domain.FolderMain, nil, 20)
			if calls, _ := st.snapshot(); calls[tc.path] != 1 || calls[tc.another] != 0 || calls["CountRooms"] != 1 {
				t.Fatalf("calls = %v, want one %s", calls, tc.path)
			}
			for _, folder := range []domain.RoomFolder{domain.FolderMain, domain.FolderArchived} {
				joined, stepwise := walkAll(t, newLister(t, st, tc.rooms), folder), walkAll(t, newLister(t, st, tc.rooms-1), folder)
				if !slices.Equal(joined, stepwise) {
					t.Errorf("folder %d: joined path and stepwise path differ", folder)
				}
				if want := expectedRooms(tc.rooms, folder); !slices.Equal(roomsOfShapes(joined), want) {
					t.Errorf("folder %d: %d rooms listed, want %d in order", folder, len(joined), len(want))
				}
			}
		})
	}
}

func expectedRooms(n int, folder domain.RoomFolder) []uint64 {
	var keys []domain.RoomSortKey
	for i := range n {
		if (i%7 == 0) != (folder == domain.FolderArchived) {
			continue
		}
		var pinned, last time.Time
		if i%11 == 0 {
			pinned = day.Add(time.Duration(i%5) * time.Minute)
		}
		if i%5 != 0 {
			last = day.Add(-time.Duration(i%89) * time.Minute)
		}
		keys = append(keys, domain.NewRoomSortKey(manyBase+uint64(i), pinned, last, day.Add(-time.Duration(i%97)*time.Hour)))
	}
	slices.SortFunc(keys, domain.CompareRoomSortKeys)
	out := make([]uint64, len(keys))
	for i, k := range keys {
		out[i] = k.Room
	}
	return out
}

func roomsOfShapes(items []itemShape) []uint64 {
	out := make([]uint64, len(items))
	for i, it := range items {
		out[i] = it.room
	}
	return out
}
