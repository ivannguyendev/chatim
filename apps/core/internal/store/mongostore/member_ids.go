package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MemberIDs = (*Store)(nil)

var memberIDFields = bson.D{{Key: "user_id", Value: 1}, {Key: "_id", Value: 0}}

type memberIDDoc struct {
	User string `bson:"user_id"`
}

func (s *Store) MemberUserIDs(ctx context.Context, room uint64) ([]string, error) {
	if err := store.ValidateMemberIDsRoom(room); err != nil {
		return nil, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	cur, err := s.members.Find(ctx, memberIDsFilter(key), options.Find().SetProjection(memberIDFields))
	if err != nil {
		return nil, fmt.Errorf("member ids of room %d: %w", room, err)
	}
	defer cur.Close(ctx)
	out := []string{}
	for cur.Next(ctx) {
		var d memberIDDoc
		if err := cur.Decode(&d); err != nil {
			return nil, fmt.Errorf("member ids of room %d: %w", room, err)
		}
		out = append(out, d.User)
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("member ids of room %d: %w", room, err)
	}
	return out, nil
}

func memberIDsFilter(room int64) bson.D {
	return bson.D{{Key: "room_id", Value: room}, {Key: "state", Value: int64(domain.MemberActive)}}
}
