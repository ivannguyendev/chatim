package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Store) TouchActivity(ctx context.Context, acts []store.Activity) error {
	models := make([]mongo.WriteModel, 0, len(acts))
	for _, a := range acts {
		room, roomErr := toInt64("room id", a.Room)
		seq, seqErr := toInt64("seq", a.Seq)
		if roomErr != nil || seqErr != nil || a.Room == 0 {
			continue
		}
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "_id", Value: room}}).
			SetUpdate(bson.D{{Key: "$max", Value: activityFields(a, seq)}}))
	}
	if len(models) == 0 {
		return ctx.Err()
	}
	if _, err := s.rooms.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
		return fmt.Errorf("touch activity of %d rooms: %w", len(models), err)
	}
	return nil
}

func activityFields(a store.Activity, seq int64) bson.D {
	at := a.At.UTC()
	fields := bson.D{{Key: "lc", Value: at}, {Key: "ab", Value: store.HourBucket(at)}}
	if a.Thread == 0 && a.Seq > 0 {
		fields = append(fields, bson.E{Key: "ls", Value: seq}, bson.E{Key: "lm", Value: at})
	}
	return fields
}

func (s *Store) ActiveRooms(ctx context.Context, q store.ActiveQuery) ([]domain.Room, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	after, err := toInt64("after", q.After)
	if err != nil {
		return nil, err
	}
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(q.Limit))
	cur, err := s.rooms.Find(ctx, activeFilter(q, after), opts)
	if err != nil {
		return nil, fmt.Errorf("active rooms after %d: %w", q.After, err)
	}
	var docs []roomDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("active rooms after %d: %w", q.After, err)
	}
	out := make([]domain.Room, 0, len(docs))
	for _, d := range docs {
		r, err := decodeRoom(d)
		if err != nil {
			return nil, fmt.Errorf("active rooms after %d: %w", q.After, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func activeFilter(q store.ActiveQuery, after int64) bson.D {
	filter := bson.D{
		{Key: "_id", Value: bson.D{{Key: "$gt", Value: after}}},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "ab", Value: bson.D{{Key: "$gte", Value: store.HourBucket(q.From)}}}},
			bson.D{{Key: "ca", Value: bson.D{{Key: "$gte", Value: q.From}, {Key: "$lte", Value: q.To}}}},
		}},
	}
	if q.Tenant != "" {
		filter = append(filter, bson.E{Key: "t", Value: q.Tenant})
	}
	return filter
}
