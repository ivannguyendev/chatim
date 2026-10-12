package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func roomInsertCases() []roomsCase {
	return []roomsCase{
		{"insert stores the room without members and with no count", roomsInsert},
		{"insert keeps the dm key", roomsInsertDirect},
		{"insert of an existing id fails and keeps the room", roomsInsertExisting},
		{"invalid inserts are rejected and not stored", roomsInsertInvalid},
	}
}

func directOf(room uint64) domain.Room {
	return domain.Room{ID: room, Tenant: tenant, Type: domain.RoomDM, CreatedBy: "lan", CreatedAt: baseTime, DMKey: domain.DirectKey(tenant, "lan", "minh")}
}

func mustInsertRoom(t *testing.T, s store.Rooms, r domain.Room) {
	t.Helper()
	if err := s.InsertRoom(t.Context(), r); err != nil {
		t.Fatalf("InsertRoom(%d): %v", r.ID, err)
	}
}

func roomsInsert(t *testing.T, s store.Rooms) {
	r := group(roomA, "Team", 5)
	r.MemberCountVer = 3
	mustInsertRoom(t, s, r)
	r.MemberCount, r.MemberCountVer = 0, 0
	assertRoom(t, s, r)
	assertNotMember(t, s, roomA, "alice")
}

func roomsInsertDirect(t *testing.T, s store.Rooms) {
	mustInsertRoom(t, s, directOf(roomA))
	assertRoom(t, s, directOf(roomA))
}

func roomsInsertExisting(t *testing.T, s store.Rooms) {
	mustInsertRoom(t, s, directOf(roomA))
	assertErrorIs(t, "InsertRoom", s.InsertRoom(t.Context(), group(roomA, "Other", 0)), apperr.ErrAlreadyExists)
	assertErrorIs(t, "InsertRoom", s.InsertRoom(t.Context(), group(roomA, "Other", 0)), store.ErrRoomExists)
	assertRoom(t, s, directOf(roomA))
}

func roomsInsertInvalid(t *testing.T, s store.Rooms) {
	keyless := directOf(roomA)
	keyless.DMKey = ""
	keyed := group(roomB, "Team", 0)
	keyed.DMKey = directOf(roomB).DMKey
	for _, r := range []domain.Room{group(0, "Team", 0), keyless, keyed} {
		assertErrorIs(t, "Insert", s.InsertRoom(t.Context(), r), apperr.ErrInvalidArgument)
		assertNoRoom(t, s, r.ID)
	}
}
