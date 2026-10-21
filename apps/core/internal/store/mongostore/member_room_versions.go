package mongostore

import (
	"context"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type memberRoomVersionDoc struct {
	Room int64 `bson:"room_id"`
	Ver  int64 `bson:"ver"`
}

var memberRoomVersionFields = bson.D{{Key: "room_id", Value: 1}, {Key: "ver", Value: 1}, {Key: "_id", Value: 0}}

var memberRoomVersionSort = bson.D{{Key: "room_id", Value: 1}}

func (s *Store) RoomVersionsOf(ctx context.Context, tenant, user string) ([]store.RoomVersion, error) {
	if err := domain.ValidUser(user); err != nil {
		return nil, err
	}
	opts := options.Find().SetProjection(memberRoomVersionFields).SetSort(memberRoomVersionSort)
	cur, err := s.members.Find(ctx, memberRoomsFilter(tenant, user), opts)
	if err != nil {
		return nil, fmt.Errorf("room versions of %q: %w", user, err)
	}
	var docs []memberRoomVersionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("room versions of %q: %w", user, err)
	}
	out := make([]store.RoomVersion, len(docs))
	for i, d := range docs {
		room, err := toUint64("member room id", d.Room)
		if err != nil {
			return nil, err
		}
		if d.Ver < 0 || d.Ver > math.MaxUint32 {
			return nil, fmt.Errorf("%w: member ver %d", errCorrupt, d.Ver)
		}
		out[i] = store.RoomVersion{Room: room, Ver: uint32(d.Ver)}
	}
	return out, nil
}
