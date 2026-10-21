package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestRoomVersionsOfIsCoveredByTheUserRoomsIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itRoomWithOwner(t, s)
	find := bson.D{
		{Key: "find", Value: membersCollection}, {Key: "filter", Value: memberRoomsFilter("acme", "alice")},
		{Key: "projection", Value: memberRoomVersionFields}, {Key: "sort", Value: memberRoomVersionSort},
	}
	stages, indexes := winningPlan(t, db, find)
	if !slices.Contains(stages, "PROJECTION_COVERED") || !slices.Contains(stages, "IXSCAN") || slices.Contains(stages, "FETCH") ||
		slices.Contains(stages, "SORT") || !slices.Contains(indexes, membersUserIndex) {
		t.Fatalf("winning plan stages %v on %v, want PROJECTION_COVERED over an IXSCAN of %s without FETCH or SORT", stages, indexes, membersUserIndex)
	}
	got, err := s.RoomVersionsOf(t.Context(), "acme", "alice")
	if err != nil || !slices.Equal(got, []store.RoomVersion{{Room: itRoom, Ver: 1}}) {
		t.Fatalf("RoomVersionsOf = %+v, %v; want room %d at ver 1", got, err, itRoom)
	}
}
