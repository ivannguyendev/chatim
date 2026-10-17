package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestListMembersScansOneRoleRangeOfTheRoomIndexWithoutSort(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itRoomWithOwner(t, s)
	cursors := map[string]store.MemberCursor{
		"first page":         {},
		"cursor in the role": {Role: domain.RoleAdmin, Priority: 5, JoinedAt: codecTime, User: "bob"},
	}
	for name, after := range cursors {
		find := bson.D{
			{Key: "find", Value: membersCollection}, {Key: "filter", Value: memberRoleFilter(int64(itRoom), domain.RoleAdmin, after)},
			{Key: "sort", Value: memberListSort}, {Key: "limit", Value: 500},
		}
		stages, indexes := winningPlan(t, db, find)
		if !slices.Contains(indexes, roomStateIndex) || slices.Contains(stages, "SORT") || slices.Contains(stages, "COLLSCAN") {
			t.Fatalf("%s plan stages %v on indexes %v, want an index scan of %s without SORT", name, stages, indexes, roomStateIndex)
		}
	}
}
