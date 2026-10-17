package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const memberRoomsIndex = "tenant_1_user_id_1_state_1_room_id_1_joined_at_1_cleared_at_1_read_seq_1_pinned_at_1_archived_at_1"

func TestRoomsOfIsCoveredByTheUserRoomsIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itRoomWithOwner(t, s)
	if _, _, err := s.ClearHistory(t.Context(), itRoom, "alice", codecTime); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	find := bson.D{
		{Key: "find", Value: membersCollection}, {Key: "filter", Value: memberRoomsFilter("acme", "alice")},
		{Key: "projection", Value: memberRoomFields}, {Key: "sort", Value: bson.D{{Key: "room_id", Value: 1}}},
	}
	stages, indexes := winningPlan(t, db, find)
	if slices.Contains(stages, "FETCH") || slices.Contains(stages, "SORT") || !slices.Contains(indexes, memberRoomsIndex) {
		t.Fatalf("winning plan stages %v on %v, want a covered %s scan without FETCH or SORT", stages, indexes, memberRoomsIndex)
	}
	got, err := s.RoomsOf(t.Context(), "acme", "alice")
	if err != nil || len(got) != 1 || got[0].Room != itRoom || !got[0].ClearedAt.Equal(codecTime) || got[0].JoinedAt.IsZero() {
		t.Fatalf("RoomsOf = %+v, %v; want room %d cleared at %v", got, err, itRoom, codecTime)
	}
}
