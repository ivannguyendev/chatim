package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var memberListSort = bson.D{{Key: "priority", Value: -1}, {Key: "joined_at", Value: 1}, {Key: "user_id", Value: 1}}

func (s *Store) ListMembers(ctx context.Context, room uint64, after store.MemberCursor, limit int) ([]domain.Member, error) {
	if err := store.ValidateMemberList(room, after, limit); err != nil {
		return nil, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Member, 0, limit)
	for _, role := range store.RolesFrom(after) {
		filter := memberRoleFilter(key, role, after)
		page, err := findMembers(ctx, s.members, filter, options.Find().SetSort(memberListSort).SetLimit(int64(limit-len(out))))
		if err != nil {
			return nil, fmt.Errorf("list %s members of room %d: %w", role, room, err)
		}
		if out = append(out, page...); len(out) == limit {
			break
		}
	}
	return out, nil
}

func memberRoleFilter(room int64, role domain.Role, after store.MemberCursor) bson.D {
	filter := bson.D{{Key: "room_id", Value: room}, {Key: "state", Value: int64(domain.MemberActive)}, {Key: "role", Value: role}}
	if after.Role != role {
		return filter
	}
	p := int64(after.Priority)
	lower := bson.D{{Key: "priority", Value: bson.D{{Key: "$lt", Value: p}}}}
	later := bson.D{{Key: "priority", Value: p}, {Key: "joined_at", Value: bson.D{{Key: "$gt", Value: after.JoinedAt}}}}
	tied := bson.D{{Key: "priority", Value: p}, {Key: "joined_at", Value: after.JoinedAt}, {Key: "user_id", Value: bson.D{{Key: "$gt", Value: after.User}}}}
	return append(filter,
		bson.E{Key: "priority", Value: bson.D{{Key: "$lte", Value: p}}},
		bson.E{Key: "$or", Value: bson.A{lower, later, tied}},
	)
}
