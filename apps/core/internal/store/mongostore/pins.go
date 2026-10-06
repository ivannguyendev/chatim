package mongostore

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type Pins struct {
	coll *mongo.Collection
}

func (p *Pins) Append(ctx context.Context, a domain.PinAction) error {
	doc, err := encodePinAction(a)
	if err != nil {
		return err
	}
	if _, err := p.coll.InsertOne(ctx, doc); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("append pin v%d of room %d: %w", a.PV, a.Room, store.ErrPinExists)
		}
		return fmt.Errorf("append pin v%d of room %d: %w", a.PV, a.Room, err)
	}
	return nil
}

func (p *Pins) At(ctx context.Context, room, pv uint64) (domain.PinAction, error) {
	var d pinActionDoc
	if err := findOne(ctx, p.coll, bson.D{{Key: "_id", Value: keys.Pin(room, pv)}}, &d, store.ErrPinNotFound); err != nil {
		return domain.PinAction{}, fmt.Errorf("pin v%d of room %d: %w", pv, room, err)
	}
	return decodePinAction(d)
}

func (p *Pins) After(ctx context.Context, room, pv uint64, limit int) ([]domain.PinAction, error) {
	if err := store.ValidateLimit(limit, store.MaxPinScan); err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "_id", Value: bson.D{
		{Key: "$gt", Value: keys.Pin(room, pv)},
		{Key: "$lte", Value: keys.Pin(room, math.MaxUint64)},
	}}}
	return p.find(ctx, filter, bson.D{{Key: "_id", Value: 1}}, limit)
}

func (p *Pins) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.PinAction, error) {
	if err := store.ValidateLimit(limit, store.MaxPinScan); err != nil {
		return nil, err
	}
	rid, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "r", Value: rid}, {Key: "ts", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	return p.find(ctx, filter, bson.D{{Key: "ts", Value: 1}, {Key: "_id", Value: 1}}, limit)
}

func (p *Pins) find(ctx context.Context, filter, sort bson.D, limit int) ([]domain.PinAction, error) {
	cur, err := p.coll.Find(ctx, filter, options.Find().SetSort(sort).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("find pin actions: %w", err)
	}
	var docs []pinActionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("find pin actions: %w", err)
	}
	return decodePinActions(docs)
}
