package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type memberRoomDoc struct {
	Room      int64     `bson:"room_id"`
	JoinedAt  time.Time `bson:"joined_at"`
	ClearedAt time.Time `bson:"cleared_at"`
	ReadSeq   int64     `bson:"read_seq"`
}

var memberRoomFields = bson.D{{Key: "room_id", Value: 1}, {Key: "joined_at", Value: 1}, {Key: "cleared_at", Value: 1}, {Key: "read_seq", Value: 1}, {Key: "_id", Value: 0}}

func (s *Store) RoomsOf(ctx context.Context, tenant, user string) ([]store.MemberRoom, error) {
	if err := domain.ValidUser(user); err != nil {
		return nil, err
	}
	cur, err := s.members.Find(ctx, memberRoomsFilter(tenant, user), options.Find().SetProjection(memberRoomFields).SetSort(bson.D{{Key: "room_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("rooms of %q: %w", user, err)
	}
	var docs []memberRoomDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("rooms of %q: %w", user, err)
	}
	out := make([]store.MemberRoom, len(docs))
	for i, d := range docs {
		room, err := toUint64("member room id", d.Room)
		if err != nil {
			return nil, err
		}
		readSeq, err := toUint64("member read seq", d.ReadSeq)
		if err != nil {
			return nil, err
		}
		out[i] = store.MemberRoom{Room: room, JoinedAt: d.JoinedAt, ClearedAt: d.ClearedAt, ReadSeq: readSeq}
	}
	return out, nil
}

func memberRoomsFilter(tenant, user string) bson.D {
	return bson.D{{Key: "tenant", Value: tenant}, {Key: "user_id", Value: user}, {Key: "state", Value: int64(domain.MemberActive)}}
}
