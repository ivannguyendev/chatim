package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type stateSeeder struct{ db *mongo.Database }

func (s stateSeeder) SeedState(ctx context.Context, target store.StateTarget, at time.Time) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if err := store.ValidateMarkTime(at); err != nil {
		return err
	}
	filter, ok := seedFilter(target)
	if !ok {
		return fmt.Errorf("seed %+v: key out of range", target)
	}
	update := bson.D{
		{Key: "$bit", Value: bson.D{{Key: stateField, Value: bson.D{{Key: "or", Value: int32(target.Bit)}}}}},
		{Key: "$min", Value: bson.D{{Key: stateAtField, Value: at.UTC().Truncate(time.Millisecond)}}},
	}
	if field, value, versioned := seedVersion(target); versioned {
		update = append(update, bson.E{Key: "$set", Value: bson.D{{Key: field, Value: value}}})
	}
	coll := s.db.Collection(target.Collection.String())
	if _, err := coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true)); err != nil {
		return fmt.Errorf("seed %+v: %w", target, err)
	}
	return nil
}

func seedFilter(t store.StateTarget) (bson.D, bool) {
	switch t.Collection {
	case store.StateHidden:
		return hiddenMatch(t)
	case store.StateRooms:
		id, ok := int64Of(t.Room)
		return bson.D{{Key: "_id", Value: id}}, ok
	default:
		return bson.D{{Key: "_id", Value: stateKey(t)}}, true
	}
}

func seedVersion(t store.StateTarget) (string, any, bool) {
	field, ok := versionField(t.Collection, t.Bit)
	switch {
	case !ok:
		return "", nil, false
	case field == clearedAtField:
		return field, t.At.UTC().Truncate(time.Millisecond), true
	default:
		return field, int64(t.Ver), true
	}
}
