package mongostore

import (
	"context"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type hiddenDoc struct {
	User   string `bson:"u"`
	Room   int64  `bson:"r"`
	Thread int64  `bson:"th"`
	Seq    int64  `bson:"s"`
}

func (s *Store) Hide(ctx context.Context, user string, key store.MsgKey) error {
	doc, err := encodeHidden(user, key)
	if err != nil {
		return err
	}
	if _, err := s.hidden.InsertOne(ctx, doc); err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("hide %d/%d/%d for %q: %w", key.Room, key.Thread, key.Seq, user, err)
	}
	return nil
}

func encodeHidden(user string, key store.MsgKey) (hiddenDoc, error) {
	if err := key.Validate(); err != nil {
		return hiddenDoc{}, err
	}
	room, err := toInt64("room id", key.Room)
	if err != nil {
		return hiddenDoc{}, err
	}
	thread, err := toInt64("thread", key.Thread)
	if err != nil {
		return hiddenDoc{}, err
	}
	seq, err := toInt64("seq", key.Seq)
	if err != nil {
		return hiddenDoc{}, err
	}
	return hiddenDoc{User: user, Room: room, Thread: thread, Seq: seq}, nil
}

func (s *Store) HiddenIn(ctx context.Context, user string, room, thread, from, to uint64) ([]uint64, error) {
	r, roomErr := toInt64("room id", room)
	th, threadErr := toInt64("thread", thread)
	lo, fromErr := toInt64("from", from)
	hi, _ := toInt64("to", min(to, math.MaxInt64))
	if roomErr != nil || threadErr != nil || fromErr != nil || from > to {
		return nil, ctx.Err()
	}
	filter := bson.D{
		{Key: "u", Value: user}, {Key: "r", Value: r}, {Key: "th", Value: th},
		{Key: "s", Value: bson.D{{Key: "$gte", Value: lo}, {Key: "$lte", Value: hi}}},
	}
	opts := options.Find().SetSort(bson.D{{Key: "s", Value: 1}}).SetProjection(bson.D{{Key: "s", Value: 1}, {Key: "_id", Value: 0}})
	cur, err := s.hidden.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("hidden of %q in %d/%d: %w", user, room, thread, err)
	}
	var docs []hiddenDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("hidden of %q in %d/%d: %w", user, room, thread, err)
	}
	var out []uint64
	for _, d := range docs {
		seq, err := toUint64("hidden seq", d.Seq)
		if err != nil {
			return nil, err
		}
		out = append(out, seq)
	}
	return out, nil
}
