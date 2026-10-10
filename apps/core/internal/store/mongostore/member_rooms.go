package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type memberRoomDoc struct {
	Room int64 `bson:"room_id"`
}

func (s *Store) RoomsOf(ctx context.Context, tenant, user string) ([]uint64, error) {
	if err := domain.ValidUser(user); err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "tenant", Value: tenant}, {Key: "user_id", Value: user}, {Key: "state", Value: int64(domain.MemberActive)}}
	opts := options.Find().SetProjection(bson.D{{Key: "room_id", Value: 1}, {Key: "_id", Value: 0}}).SetSort(bson.D{{Key: "room_id", Value: 1}})
	cur, err := s.members.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("rooms of %q: %w", user, err)
	}
	var docs []memberRoomDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("rooms of %q: %w", user, err)
	}
	out := make([]uint64, len(docs))
	for i, d := range docs {
		room, err := toUint64("member room id", d.Room)
		if err != nil {
			return nil, err
		}
		out[i] = room
	}
	return out, nil
}
