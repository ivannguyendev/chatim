package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type reactorRow struct {
	Actor     string    `bson:"actor_id"`
	Value     string    `bson:"value"`
	UpdatedAt time.Time `bson:"updated_at"`
}

var reactorProjection = bson.D{{Key: "_id", Value: 0}, {Key: "actor_id", Value: 1}, {Key: "value", Value: 1}, {Key: "updated_at", Value: 1}}

func (r *Interactions) Reactors(ctx context.Context, q store.ReactorQuery) ([]store.Reactor, error) {
	if err := store.ValidateReactorQuery(q); err != nil {
		return nil, err
	}
	filter, sort := reactorQuery(q)
	opts := options.Find().SetSort(sort).SetProjection(reactorProjection).SetLimit(int64(q.Limit))
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("reactors of %d/%d/%d: %w", q.Key.Room, q.Key.Thread, q.Key.Seq, err)
	}
	var rows []reactorRow
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("reactors of %d/%d/%d: %w", q.Key.Room, q.Key.Thread, q.Key.Seq, err)
	}
	out := make([]store.Reactor, len(rows))
	for i, row := range rows {
		out[i] = store.Reactor{User: row.Actor, Emoji: row.Value, At: row.UpdatedAt.UTC()}
	}
	return out, nil
}

func reactorQuery(q store.ReactorQuery) (filter, sort bson.D) {
	byTime := bson.D{{Key: "updated_at", Value: -1}, {Key: "actor_id", Value: 1}}
	if q.Emoji != "" {
		return reactorBranches(q.Key, reactorTimeAfter(bson.E{Key: "value", Value: q.Emoji}, q.After)), byTime
	}
	var branches []bson.D
	if c := q.After; c.User != "" {
		later := bson.D{{Key: "value", Value: bson.D{{Key: "$gt", Value: c.Emoji}}}}
		branches = append(reactorTimeAfter(bson.E{Key: "value", Value: c.Emoji}, c), later)
	}
	return reactorBranches(q.Key, branches), append(bson.D{{Key: "value", Value: 1}}, byTime...)
}

func reactorTimeAfter(value bson.E, c store.ReactorCursor) []bson.D {
	if c.User == "" {
		return []bson.D{{value}}
	}
	older := bson.D{value, {Key: "updated_at", Value: bson.D{{Key: "$lt", Value: c.At}}}}
	tied := bson.D{value, {Key: "updated_at", Value: c.At}, {Key: "actor_id", Value: bson.D{{Key: "$gt", Value: c.User}}}}
	return []bson.D{tied, older}
}

func reactorBranches(key store.MsgKey, branches []bson.D) bson.D {
	switch len(branches) {
	case 0:
		return liveOf(key, keys.ReactionKind)
	case 1:
		return append(liveOf(key, keys.ReactionKind), branches[0]...)
	}
	or := make(bson.A, len(branches))
	for i, b := range branches {
		or[i] = append(liveOf(key, keys.ReactionKind), b...)
	}
	return bson.D{{Key: "$or", Value: or}}
}
