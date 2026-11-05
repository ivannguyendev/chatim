package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMemberUserIDsIsCoveredByTheRoomStateIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itRoomWithOwner(t, s)
	find := bson.D{{Key: "find", Value: membersCollection}, {Key: "filter", Value: memberIDsFilter(int64(itRoom))}, {Key: "projection", Value: memberIDFields}}
	stages, indexes := winningPlan(t, db, find)
	if slices.Contains(stages, "FETCH") || !slices.Contains(indexes, roomStateIndex) {
		t.Fatalf("winning plan stages %v on %v, want a covered %s scan without FETCH", stages, indexes, roomStateIndex)
	}
	got, err := s.MemberUserIDs(t.Context(), itRoom)
	if err != nil || !slices.Equal(got, []string{"alice"}) {
		t.Fatalf("MemberUserIDs = %v, %v; want [alice]", got, err)
	}
}
