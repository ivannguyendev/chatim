package storetest

import (
	"maps"
	"math"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func hiddenInRooms(t *testing.T, s editStores) {
	mustHide(t, s.hidden,
		hiddenMark("bob", roomA, mainThread, 2, 1), hiddenMark("bob", roomA, mainThread, 5, 1), hiddenMark("bob", roomA, mainThread, 9, 1),
		hiddenMark("bob", roomA, mainThread, 10, 1), hiddenMark("bob", roomA, sideThread, 6, 1), hiddenMark("carol", roomA, mainThread, 6, 1),
		hiddenMark("bob", roomB, mainThread, 1, 1), hiddenMark("bob", roomB, mainThread, 4, 1), hiddenMark("carol", roomC, mainThread, 3, 1),
	)
	ranges := []store.HiddenRange{{Room: roomA, From: 3, To: 9}, {Room: roomB, From: 1, To: 3}, {Room: roomC, From: 1, To: 10}}
	got, err := s.hidden.HiddenInRooms(t.Context(), "bob", ranges)
	if err != nil {
		t.Fatalf("HiddenInRooms: %v", err)
	}
	want := map[uint64][]uint64{roomA: {5, 9}, roomB: {1}}
	if !maps.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("HiddenInRooms(bob) = %v, want %v", got, want)
	}
	if got, err := s.hidden.HiddenInRooms(t.Context(), "bob", nil); err != nil || len(got) != 0 {
		t.Fatalf("HiddenInRooms(no ranges) = %v, %v; want none", got, err)
	}
	many := make([]store.HiddenRange, store.MaxHiddenScan+1)
	for i := range many {
		many[i] = store.HiddenRange{Room: roomA, From: 1, To: 2}
	}
	for name, c := range map[string]struct {
		user   string
		ranges []store.HiddenRange
	}{
		"from above to": {"bob", []store.HiddenRange{{Room: roomA, From: 5, To: 4}}},
		"room 0":        {"bob", []store.HiddenRange{{From: 1, To: 4}}},
		"huge to":       {"bob", []store.HiddenRange{{Room: roomA, From: 1, To: math.MaxUint64}}},
		"bad user":      {"b b", ranges},
		"too many":      {"bob", many},
	} {
		_, err := s.hidden.HiddenInRooms(t.Context(), c.user, c.ranges)
		assertErrorIs(t, "HiddenInRooms("+name+")", err, apperr.ErrInvalidArgument)
	}
}
