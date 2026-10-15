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

type EventStates struct {
	colls map[store.StateCollection]*mongo.Collection
}

func newEventStates(db *mongo.Database, opts *options.CollectionOptionsBuilder) *EventStates {
	colls := make(map[store.StateCollection]*mongo.Collection, len(store.AllStateCollections()))
	for _, c := range store.AllStateCollections() {
		colls[c] = db.Collection(c.String(), opts)
	}
	return &EventStates{colls: colls}
}

func (e *EventStates) Pending(ctx context.Context, c store.StateCollection, from, before time.Time, after store.PendingCursor, limit int) ([]store.PendingDoc, store.PendingCursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, store.PendingCursor{}, err
	}
	if err := store.ValidatePendingScan(c, limit); err != nil {
		return nil, store.PendingCursor{}, err
	}
	filter, ok, err := pendingFilter(c, from, before, after)
	if err != nil || !ok {
		return nil, store.PendingCursor{}, err
	}
	opts := options.Find().SetSort(pendingSort).SetProjection(pendingProjection(c)).SetLimit(int64(limit) + 1)
	cur, err := e.colls[c].Find(ctx, filter, opts)
	if err != nil {
		return nil, store.PendingCursor{}, fmt.Errorf("pending %s: %w", c, err)
	}
	var rows []pendingRow
	if err := cur.All(ctx, &rows); err != nil {
		return nil, store.PendingCursor{}, fmt.Errorf("pending %s: %w", c, err)
	}
	return pendingPage(c, rows, limit)
}

func pendingPage(c store.StateCollection, rows []pendingRow, limit int) ([]store.PendingDoc, store.PendingCursor, error) {
	more := len(rows) > limit
	rows = rows[:min(len(rows), limit)]
	docs := make([]store.PendingDoc, len(rows))
	var next store.PendingCursor
	for i, row := range rows {
		d, key, err := decodePending(c, row)
		if err != nil {
			return nil, store.PendingCursor{}, err
		}
		docs[i] = d
		if more && i == len(rows)-1 {
			next = store.PendingCursor{At: d.StateAt, Key: key}
		}
	}
	return docs, next, nil
}

func (e *EventStates) Clear(ctx context.Context, targets []store.StateTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateStateTargets(targets); err != nil {
		return err
	}
	for _, b := range stateBatches(targets) {
		if err := e.update(ctx, b, clearStatePipeline(b.bit), bitIsSet(b.bit)); err != nil {
			return fmt.Errorf("clear %s bit %d: %w", b.coll, b.bit, err)
		}
	}
	return nil
}

func (e *EventStates) Mark(ctx context.Context, c store.StateCollection, targets []store.StateTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateMarkTargets(c, targets); err != nil {
		return err
	}
	for _, b := range stateBatches(targets) {
		if err := e.update(ctx, b, flaggedSet(b.bit)); err != nil {
			return fmt.Errorf("mark %s bit %d: %w", b.coll, b.bit, err)
		}
	}
	return nil
}

func (e *EventStates) update(ctx context.Context, b stateBatch, pipeline mongo.Pipeline, extra ...bson.E) error {
	filter, ok := stateFilter(b.coll, b.bit, b.targets, extra...)
	if !ok {
		return nil
	}
	_, err := e.colls[b.coll].UpdateMany(ctx, filter, pipeline)
	return err
}

func clearStatePipeline(bit store.StateBit) mongo.Pipeline {
	cleared := bson.D{{Key: "$bitAnd", Value: bson.A{"$" + stateField, ^bitValue(bit)}}}
	return mongo.Pipeline{
		{{Key: "$set", Value: bson.D{{Key: stateField, Value: cleared}}}},
		{{Key: "$set", Value: bson.D{{Key: stateField, Value: removeWhenClear(stateField)}, {Key: stateAtField, Value: removeWhenClear(stateAtField)}}}},
	}
}

func removeWhenClear(field string) bson.D {
	isClear := bson.D{{Key: "$eq", Value: bson.A{"$" + stateField, int32(0)}}}
	return bson.D{{Key: "$cond", Value: bson.A{isClear, "$$REMOVE", "$" + field}}}
}
