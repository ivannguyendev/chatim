package storetest

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func viewerCases() []editCase {
	return []editCase{
		{"hide is idempotent and per user and timeline", hidePerViewer},
		{"clear history only moves forward and is per member", clearForward},
		{"clear history of a non member fails and adds nobody", clearNotMember},
		{"hide again keeps the first hide time", hideKeepsFirstTime},
		{"hidden between returns the room hides in a time range by time then key", hiddenBetween},
		{"hidden between rejects a bad limit and a bad time", hiddenInvalid},
	}
}

func hidePerViewer(t *testing.T, s editStores) {
	hide := func(user string, k store.MsgKey) {
		t.Helper()
		if err := s.hidden.Hide(t.Context(), user, k, baseTime); err != nil {
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
	assertErrorIs(t, "Hide(seq 0)", s.hidden.Hide(t.Context(), "bob", msgKey(roomA, mainThread, 0), baseTime), apperr.ErrInvalidArgument)
}

func clearForward(t *testing.T, s editStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	at := func(sec int) time.Time { return baseTime.Add(time.Duration(sec) * time.Second) }
	steps := []struct {
		at, want time.Time
		rose     bool
	}{{at(5), at(5), true}, {at(3), at(5), false}, {at(9), at(9), true}, {at(9), at(9), false}}
	for _, step := range steps {
		got, rose, err := s.rooms.ClearHistory(t.Context(), roomA, "alice", step.at)
		if err != nil || !got.Equal(step.want) || rose != step.rose {
			t.Fatalf("ClearHistory(alice, %v) = %v, %v, %v; want %v, %v", step.at, got, rose, err, step.want, step.rose)
		}
	}
	alice := created(members[0])
	alice.ClearedAt, alice.LastChangeAt = at(9), at(9)
	assertMember(t, s.rooms, alice)
	assertMember(t, s.rooms, created(members[1]))
	_, _, err := s.rooms.ClearHistory(t.Context(), roomA, "bob", time.Time{})
	assertErrorIs(t, "ClearHistory(zero time)", err, apperr.ErrInvalidArgument)
	assertMember(t, s.rooms, created(members[1]))
}

func clearNotMember(t *testing.T, s editStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	_, _, err := s.rooms.ClearHistory(t.Context(), roomA, "carol", baseTime)
	assertErrorIs(t, "ClearHistory(non member)", err, domain.ErrNotMember)
	_, _, err = s.rooms.ClearHistory(t.Context(), roomB, "alice", baseTime)
	assertErrorIs(t, "ClearHistory(missing room)", err, domain.ErrNotMember)
	assertNotMember(t, s.rooms, roomA, "carol")
	assertNotMember(t, s.rooms, roomB, "alice")
}
