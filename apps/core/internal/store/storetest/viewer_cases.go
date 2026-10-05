package storetest

import (
	"math"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func viewerCases() []editCase {
	return []editCase{
		{"hide is idempotent and per user and timeline", hidePerViewer},
		{"clear history only moves forward and is per member", clearForward},
		{"clear history of a non member fails and adds nobody", clearNotMember},
	}
}

func hidePerViewer(t *testing.T, s editStores) {
	hide := func(user string, k store.MsgKey) {
		t.Helper()
		if err := s.hidden.Hide(t.Context(), user, k); err != nil {
			t.Fatalf("Hide(%q, %+v): %v", user, k, err)
		}
	}
	hide("bob", msgKey(roomA, mainThread, 7))
	hide("bob", msgKey(roomA, mainThread, 3))
	hide("bob", msgKey(roomA, mainThread, 3))
	hide("bob", msgKey(roomA, sideThread, 4))
	hide("bob", msgKey(roomB, mainThread, 5))
	hide("carol", msgKey(roomA, mainThread, 5))
	cases := []struct {
		user                   string
		room, thread, from, to uint64
		want                   []uint64
	}{
		{"bob", roomA, mainThread, 1, 10, []uint64{3, 7}},
		{"bob", roomA, mainThread, 4, 7, []uint64{7}},
		{"bob", roomA, mainThread, 8, 10, nil},
		{"bob", roomA, mainThread, 7, 3, nil},
		{"bob", roomA, sideThread, 1, 10, []uint64{4}},
		{"bob", roomB, mainThread, 1, math.MaxUint64, []uint64{5}},
		{"carol", roomA, mainThread, 1, 10, []uint64{5}},
		{"dave", roomA, mainThread, 1, 10, nil},
	}
	for _, c := range cases {
		got, err := s.hidden.HiddenIn(t.Context(), c.user, c.room, c.thread, c.from, c.to)
		if err != nil || !slices.Equal(got, c.want) {
			t.Fatalf("HiddenIn(%q, %d, %d, %d..%d) = %v, %v; want %v", c.user, c.room, c.thread, c.from, c.to, got, err, c.want)
		}
	}
	assertErrorIs(t, "Hide(seq 0)", s.hidden.Hide(t.Context(), "bob", msgKey(roomA, mainThread, 0)), apperr.ErrInvalidArgument)
}

func clearForward(t *testing.T, s editStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	for _, step := range []struct{ seq, want uint64 }{{5, 5}, {3, 5}, {9, 9}, {0, 9}} {
		got, err := s.rooms.ClearHistory(t.Context(), roomA, "alice", step.seq)
		if err != nil || got != step.want {
			t.Fatalf("ClearHistory(alice, %d) = %d, %v; want %d", step.seq, got, err, step.want)
		}
	}
	alice := members[0]
	alice.ClearedBeforeSeq = 9
	assertMember(t, s.rooms, alice)
	assertMember(t, s.rooms, members[1])
}

func clearNotMember(t *testing.T, s editStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	_, err := s.rooms.ClearHistory(t.Context(), roomA, "carol", 1)
	assertErrorIs(t, "ClearHistory(non member)", err, domain.ErrNotMember)
	_, err = s.rooms.ClearHistory(t.Context(), roomB, "alice", 1)
	assertErrorIs(t, "ClearHistory(missing room)", err, domain.ErrNotMember)
	assertNotMember(t, s.rooms, roomA, "carol")
	assertNotMember(t, s.rooms, roomB, "alice")
}
