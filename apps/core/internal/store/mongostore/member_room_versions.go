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
	Room  int64 `bson:"room_id"`
	Ver   int64 `bson:"ver"`
	State int64 `bson:"state"`
}

var memberRoomVersionFields = bson.D{{Key: "room_id", Value: 1}, {Key: "ver", Value: 1}, {Key: "state", Value: 1}, {Key: "_id", Value: 0}}

func memberRoomVersionsFilter(tenant, user string) bson.D {
	states := bson.A{int64(domain.MemberActive), int64(domain.MemberRemoved)}
	return bson.D{{Key: "tenant", Value: tenant}, {Key: "user_id", Value: user}, {Key: "state", Value: bson.D{{Key: "$in", Value: states}}}}
}

func (s *Store) RoomVersionsOf(ctx context.Context, tenant, user string) ([]store.RoomVersion, error) {
	if err := domain.ValidUser(user); err != nil {
		return nil, err
	}
	opts := options.Find().SetProjection(memberRoomVersionFields)
	cur, err := s.members.Find(ctx, memberRoomVersionsFilter(tenant, user), opts)
	if err != nil {
		return nil, fmt.Errorf("room versions of %q: %w", user, err)
	}
	var docs []memberRoomVersionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("room versions of %q: %w", user, err)
	}
	out := make([]store.RoomVersion, len(docs))
	for i, d := range docs {
		v, err := toRoomVersion(d)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func toRoomVersion(d memberRoomVersionDoc) (store.RoomVersion, error) {
	room, err := toUint64("member room id", d.Room)
	if err != nil {
		return store.RoomVersion{}, err
	}
	if d.Ver < 0 || d.Ver > math.MaxUint32 {
		return store.RoomVersion{}, fmt.Errorf("%w: member ver %d", errCorrupt, d.Ver)
	}
	return store.RoomVersion{Room: room, Ver: uint32(d.Ver), Left: d.State == int64(domain.MemberRemoved)}, nil
}
