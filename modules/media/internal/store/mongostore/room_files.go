package mongostore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func roomEquality(q domain.RoomFilesQuery) bson.D {
	return bson.D{
		{Key: "tenant_id", Value: q.Tenant}, {Key: "room_id", Value: q.Room},
		{Key: "kind", Value: string(q.Kind)}, {Key: "state", Value: int32(domain.PairLive)},
	}
}

func roomFilter(q domain.RoomFilesQuery) bson.D {
	newer := bson.D{}
	if !q.ClearedAt.IsZero() {
		newer = append(newer, bson.E{Key: "$gt", Value: q.ClearedAt})
	}
	if q.Before.IsZero() {
		if len(newer) == 0 {
			return roomEquality(q)
		}
		return append(roomEquality(q), bson.E{Key: "sent_at", Value: newer})
	}
	older := append(bson.D{{Key: "$lt", Value: q.Before.SentAt}}, newer...)
	tie := append(roomEquality(q),
		bson.E{Key: "sent_at", Value: q.Before.SentAt},
		bson.E{Key: "_id", Value: bson.D{{Key: "$lt", Value: pairID(q.Before.MessageKey, q.Before.FileKey)}}},
	)
	return bson.D{{Key: "$or", Value: bson.A{append(roomEquality(q), bson.E{Key: "sent_at", Value: older}), tie}}}
}

var (
	roomSort       = bson.D{{Key: "sent_at", Value: -1}, {Key: "_id", Value: -1}}
	roomProjection = bson.D{{Key: "_id", Value: 1}, {Key: "sent_at", Value: 1}}
)

func (s *MessageFiles) ListRoom(ctx context.Context, q domain.RoomFilesQuery) ([]domain.MessageFile, error) {
	opts := options.Find().SetSort(roomSort).SetLimit(int64(q.Limit)).SetHint(filesByRoom).SetProjection(roomProjection)
	cur, err := s.coll.Find(ctx, roomFilter(q), opts)
	if err != nil {
		return nil, fmt.Errorf("list room files: %w", err)
	}
	var docs []struct {
		ID     string    `bson:"_id"`
		SentAt time.Time `bson:"sent_at"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("list room files: %w", err)
	}
	out := make([]domain.MessageFile, len(docs))
	for i, d := range docs {
		messageKey, fileKey, ok := strings.Cut(d.ID, "|")
		if !ok {
			return nil, fmt.Errorf("list room files: pair id %q has no file key", d.ID)
		}
		out[i] = domain.MessageFile{
			MessageKey: messageKey, FileKey: fileKey, Tenant: q.Tenant, Room: q.Room, Kind: q.Kind,
			SentAt: d.SentAt.UTC(), State: domain.PairLive,
		}
	}
	return out, nil
}
