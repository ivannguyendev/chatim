package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type SweepCheckpoints struct {
	coll *mongo.Collection
}

type checkpointDoc struct {
	Checkpoint time.Time `bson:"checkpoint"`
}

func (s *SweepCheckpoints) Get(ctx context.Context, c store.StateCollection) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	if err := store.ValidateStateCollection(c); err != nil {
		return time.Time{}, err
	}
	var doc checkpointDoc
	err := s.coll.FindOne(ctx, bson.D{{Key: "_id", Value: c.String()}}).Decode(&doc)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return time.Time{}, nil
	case err != nil:
		return time.Time{}, fmt.Errorf("sweep checkpoint %s: %w", c, err)
	}
	return doc.Checkpoint.UTC(), nil
}

func (s *SweepCheckpoints) Advance(ctx context.Context, c store.StateCollection, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateCheckpoint(c, at); err != nil {
		return err
	}
	update := bson.D{
		{Key: "$max", Value: bson.D{{Key: "checkpoint", Value: toMillis(at)}}},
		{Key: "$currentDate", Value: bson.D{{Key: "updated_at", Value: true}}},
	}
	if _, err := s.coll.UpdateOne(ctx, bson.D{{Key: "_id", Value: c.String()}}, update, options.UpdateOne().SetUpsert(true)); err != nil {
		return fmt.Errorf("advance sweep checkpoint %s: %w", c, err)
	}
	return nil
}
