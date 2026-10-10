package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type DirectRooms struct {
	coll *mongo.Collection
}

type directRoomDoc struct {
	ID        string    `bson:"_id"`
	RoomID    int64     `bson:"room_id"`
	CreatedAt time.Time `bson:"created_at"`
}

func (d *DirectRooms) Claim(ctx context.Context, tenant, a, b string, candidate uint64, at time.Time) (uint64, error) {
	if err := store.ValidateDirectClaim(tenant, a, b, candidate, at); err != nil {
		return 0, err
	}
	room, err := toInt64("room id", candidate)
	if err != nil {
		return 0, err
	}
	key := domain.DirectKey(tenant, a, b)
	update := bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "room_id", Value: room}, {Key: "created_at", Value: at}}}}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	var doc directRoomDoc
	err = d.coll.FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: key}}, update, opts).Decode(&doc)
	switch {
	case mongo.IsDuplicateKeyError(err):
		return 0, fmt.Errorf("claim direct room %s: %w", key, domain.ErrRetryLater)
	case err != nil:
		return 0, fmt.Errorf("claim direct room %s: %w", key, err)
	}
	return toUint64("direct room id", doc.RoomID)
}

func (d *DirectRooms) Repoint(ctx context.Context, tenant, a, b string, old, next uint64) (bool, error) {
	if err := store.ValidateDirectRepoint(tenant, a, b, next); err != nil {
		return false, err
	}
	from, err := toInt64("room id", old)
	if err != nil {
		return false, err
	}
	to, err := toInt64("room id", next)
	if err != nil {
		return false, err
	}
	key := domain.DirectKey(tenant, a, b)
	filter := bson.D{{Key: "_id", Value: key}, {Key: "room_id", Value: from}}
	res, err := d.coll.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "room_id", Value: to}}}})
	if err != nil {
		return false, fmt.Errorf("repoint direct room %s from %d to %d: %w", key, old, next, err)
	}
	return res.MatchedCount == 1, nil
}
