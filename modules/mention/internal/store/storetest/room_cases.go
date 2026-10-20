package storetest

import (
	"maps"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
)

func RunRooms(t *testing.T, open func(t *testing.T) store.Rooms) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, s store.Rooms)
	}{
		{"RaiseOnlyMovesForward", roomRaiseForward},
		{"TimesListsOnlyRoomsThatHadOneAcrossChunks", roomTimes},
		{"AnotherTenantNeverSeesOrMovesTheRoom", roomTenantsApart},
		{"InvalidCallsAreRejected", roomInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func mustRaise(t *testing.T, s store.Rooms, ten string, room uint64, at time.Time) {
	t.Helper()
	if err := s.Raise(t.Context(), ten, room, at); err != nil {
		t.Fatalf("Raise(%s, %d, %v) = %v", ten, room, at, err)
	}
}

func assertTimes(t *testing.T, s store.Rooms, ten string, rooms []uint64, want map[uint64]time.Time) {
	t.Helper()
	got, err := s.Times(t.Context(), ten, rooms)
	if err != nil || !maps.EqualFunc(got, want, time.Time.Equal) {
		t.Fatalf("Times(%s, %d rooms) = %v, %v; want %v", ten, len(rooms), got, err, want)
	}
}

func roomRaiseForward(t *testing.T, s store.Rooms) {
	mustRaise(t, s, tenant, roomA, secs(20))
	mustRaise(t, s, tenant, roomA, secs(10))
	assertTimes(t, s, tenant, []uint64{roomA}, map[uint64]time.Time{roomA: secs(20)})
	mustRaise(t, s, tenant, roomA, secs(30))
	assertTimes(t, s, tenant, []uint64{roomA}, map[uint64]time.Time{roomA: secs(30)})
}

func roomTimes(t *testing.T, s store.Rooms) {
	far := roomA + store.SharedMentionChunk + 5
	mustRaise(t, s, tenant, roomA, secs(10))
	mustRaise(t, s, tenant, far, secs(40))
	ids := make([]uint64, 0, store.SharedMentionChunk+10)
	for i := range uint64(store.SharedMentionChunk + 10) {
		ids = append(ids, roomA+i)
	}
	assertTimes(t, s, tenant, ids, map[uint64]time.Time{roomA: secs(10), far: secs(40)})
	assertTimes(t, s, tenant, nil, map[uint64]time.Time{})
}

func roomTenantsApart(t *testing.T, s store.Rooms) {
	mustRaise(t, s, tenant, roomA, secs(10))
	mustRaise(t, s, otherTenant, roomA, secs(50))
	mustRaise(t, s, otherTenant, roomB, secs(60))
	assertTimes(t, s, tenant, []uint64{roomA, roomB}, map[uint64]time.Time{roomA: secs(10)})
	assertTimes(t, s, otherTenant, []uint64{roomA, roomB}, map[uint64]time.Time{roomB: secs(60)})
}

func roomInvalid(t *testing.T, s store.Rooms) {
	assertInvalid(t, "Raise(room 0)", s.Raise(t.Context(), tenant, 0, secs(1)))
	assertInvalid(t, "Raise(zero time)", s.Raise(t.Context(), tenant, roomA, time.Time{}))
	assertInvalid(t, "Raise(no tenant)", s.Raise(t.Context(), "", roomA, secs(1)))
	_, err := s.Times(t.Context(), tenant, []uint64{roomA, 0})
	assertInvalid(t, "Times(room 0)", err)
	_, err = s.Times(t.Context(), "", []uint64{roomA})
	assertInvalid(t, "Times(no tenant)", err)
	assertTimes(t, s, tenant, []uint64{roomA}, map[uint64]time.Time{})
}
