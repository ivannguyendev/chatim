package mongostore

import (
	"cmp"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func itLeftRoom(t *testing.T, s *Store, room uint64) {
	t.Helper()
	r := domain.Room{ID: room, Tenant: "acme", Type: domain.RoomGroup, Name: "Gone", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 1}
	if err := s.Create(t.Context(), r, []domain.Member{{Room: room, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: codecTime}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cur, err := s.MembersOf(t.Context(), room, []string{"alice"})
	if err != nil || len(cur) != 1 {
		t.Fatalf("MembersOf = %v, %v; want alice", cur, err)
	}
	next := cur[0].Next(cur[0].Role, domain.MemberRemoved, 0, "req-leave", "alice", codecTime)
	if ok, err := s.ApplyMember(t.Context(), cur[0], next); err != nil || !ok {
		t.Fatalf("ApplyMember(leave) = %v, %v; want applied", ok, err)
	}
}

func TestRoomVersionsOfIsCoveredByTheUserRoomsIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itRoomWithOwner(t, s)
	itLeftRoom(t, s, itRoom+1)
	find := bson.D{
		{Key: "find", Value: membersCollection}, {Key: "filter", Value: memberRoomVersionsFilter("acme", "alice")},
		{Key: "projection", Value: memberRoomVersionFields},
	}
	stages, indexes := winningPlan(t, db, find)
	if !slices.Contains(stages, "PROJECTION_COVERED") || !slices.Contains(stages, "IXSCAN") || slices.Contains(stages, "FETCH") ||
		slices.Contains(stages, "SORT") || slices.Contains(stages, "SORT_MERGE") || !slices.Contains(indexes, membersUserIndex) {
		t.Fatalf("winning plan stages %v on %v, want PROJECTION_COVERED over an IXSCAN of %s without FETCH or SORT", stages, indexes, membersUserIndex)
	}
	got, err := s.RoomVersionsOf(t.Context(), "acme", "alice")
	slices.SortFunc(got, compareRoomVersions)
	want := []store.RoomVersion{{Room: itRoom, Ver: 1}, {Room: itRoom + 1, Ver: 2, Left: true}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("RoomVersionsOf = %+v, %v; want %+v", got, err, want)
	}
}

func compareRoomVersions(a, b store.RoomVersion) int {
	if a.Left != b.Left {
		if a.Left {
			return 1
		}
		return -1
	}
	return cmp.Compare(a.Room, b.Room)
}
