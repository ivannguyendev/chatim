package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func activityCases() []roomsCase {
	return []roomsCase{
		{"touch activity keeps the highest seq and time and never moves back", activityMonotonic},
		{"thread activity only bumps the change time and bucket", activityThread},
		{"an activity with seq 0 bumps only the last change", activityWithoutSeq},
		{"touch of a missing room is ignored", activityMissingRoom},
		{"active rooms: touched since from or created in range, by tenant, paged by id", activeRoomsRange},
		{"active rooms rejects a bad limit or range", activeRoomsInvalid},
	}
}

func roomAt(id uint64, tenantID string, created time.Time) (domain.Room, []domain.Member) {
	r := domain.Room{ID: id, Tenant: tenantID, Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	return r, []domain.Member{{Room: id, Tenant: tenantID, User: "alice", Role: domain.RoleOwner, JoinedAt: created}}
}

func mustTouch(t *testing.T, s store.Rooms, acts ...store.Activity) {
	t.Helper()
	if err := s.TouchActivity(t.Context(), acts); err != nil {
		t.Fatalf("TouchActivity(%+v): %v", acts, err)
	}
}

func assertActivity(t *testing.T, s store.Rooms, id, seq uint64, msgAt, changeAt time.Time) {
	t.Helper()
	got, err := s.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	if got.LastSeq != seq || !got.LastMsgAt.Equal(msgAt) || !got.LastChangeAt.Equal(changeAt) {
		t.Fatalf("room %d activity = seq %d, msg %v, change %v; want seq %d, msg %v, change %v",
			id, got.LastSeq, got.LastMsgAt, got.LastChangeAt, seq, msgAt, changeAt)
	}
}

func activeIDs(t *testing.T, s store.Rooms, q store.ActiveQuery) []uint64 {
	t.Helper()
	rooms, err := s.ActiveRooms(t.Context(), q)
	if err != nil {
		t.Fatalf("ActiveRooms(%+v): %v", q, err)
	}
	out := make([]uint64, len(rooms))
	for i, r := range rooms {
		out[i] = r.ID
	}
	return out
}

func activityMonotonic(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	early, late := baseTime.Add(time.Hour), baseTime.Add(2*time.Hour)
	mustTouch(t, s, store.Activity{Room: roomA, Seq: 5, At: late}, store.Activity{Room: roomA, Seq: 3, At: early})
	assertActivity(t, s, roomA, 5, late, late)
	mustTouch(t, s, store.Activity{Room: roomA, Seq: 4, At: early})
	assertActivity(t, s, roomA, 5, late, late)
	mustTouch(t, s)
	assertActivity(t, s, roomA, 5, late, late)
}

func activityThread(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	threadAt := baseTime.Add(3 * time.Hour)
	mustTouch(t, s, store.Activity{Room: roomA, Seq: 2, At: baseTime})
	mustTouch(t, s, store.Activity{Room: roomA, Thread: sideThread, Seq: 50, At: threadAt})
	assertActivity(t, s, roomA, 2, baseTime, threadAt)
	if got := activeIDs(t, s, store.ActiveQuery{From: threadAt, To: threadAt, Limit: 10}); !slices.Equal(got, []uint64{roomA}) {
		t.Fatalf("active rooms in the thread's hour = %v, want [%d]", got, roomA)
	}
}

func activityMissingRoom(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	mustTouch(t, s, store.Activity{Room: roomB, Seq: 3, At: baseTime}, store.Activity{Room: roomA, Seq: 1, At: baseTime})
	assertNoRoom(t, s, roomB)
	assertActivity(t, s, roomA, 1, baseTime, baseTime)
}

func activeRoomsRange(t *testing.T, s store.Rooms) {
	old := baseTime.Add(-48 * time.Hour)
	for _, r := range []struct {
		id       uint64
		tenantID string
		created  time.Time
	}{
		{roomA, tenant, baseTime.Add(10 * time.Minute)},
		{roomB, tenant, old},
		{roomB + 1, tenant, old},
		{roomB + 2, tenant, old},
		{roomB + 3, "other", baseTime},
	} {
		room, members := roomAt(r.id, r.tenantID, r.created)
		mustCreate(t, s, room, members)
	}
	mustTouch(t, s,
		store.Activity{Room: roomB, Seq: 9, At: baseTime.Add(5 * time.Hour)},
		store.Activity{Room: roomB + 2, Seq: 4, At: baseTime.Add(-3 * time.Hour)},
	)
	q := store.ActiveQuery{From: baseTime, To: baseTime.Add(time.Hour), Limit: 10}
	steps := []struct {
		name string
		edit func(*store.ActiveQuery)
		want []uint64
	}{
		{"every tenant", func(*store.ActiveQuery) {}, []uint64{roomA, roomB, roomB + 3}},
		{"one tenant", func(q *store.ActiveQuery) { q.Tenant = tenant }, []uint64{roomA, roomB}},
		{"first page", func(q *store.ActiveQuery) { q.Limit = 1 }, []uint64{roomA}},
		{"second page", func(q *store.ActiveQuery) { q.After = roomA }, []uint64{roomB}},
		{"past the last page", func(q *store.ActiveQuery) { q.After = roomB }, []uint64{}},
	}
	for _, step := range steps {
		step.edit(&q)
		if got := activeIDs(t, s, q); !slices.Equal(got, step.want) {
			t.Fatalf("%s: ActiveRooms(%+v) = %v, want %v", step.name, q, got, step.want)
		}
	}
}

func activeRoomsInvalid(t *testing.T, s store.Rooms) {
	for _, q := range []store.ActiveQuery{
		{From: baseTime, To: baseTime, Limit: 0},
		{From: baseTime, To: baseTime, Limit: store.MaxActiveLimit + 1},
		{From: baseTime, To: baseTime.Add(-time.Second), Limit: 1},
	} {
		_, err := s.ActiveRooms(t.Context(), q)
		assertErrorIs(t, "ActiveRooms", err, apperr.ErrInvalidArgument)
	}
}

func activityWithoutSeq(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	editAt := baseTime.Add(5 * time.Hour)
	mustTouch(t, s, store.Activity{Room: roomA, Seq: 2, At: baseTime})
	mustTouch(t, s, store.Activity{Room: roomA, At: editAt})
	assertActivity(t, s, roomA, 2, baseTime, editAt)
	if got := activeIDs(t, s, store.ActiveQuery{From: editAt, To: editAt, Limit: 10}); !slices.Equal(got, []uint64{roomA}) {
		t.Fatalf("active rooms in the edit's hour = %v, want [%d]", got, roomA)
	}
}
