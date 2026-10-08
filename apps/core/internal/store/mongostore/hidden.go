package mongostore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type Hidden struct {
	coll *mongo.Collection
}

type hiddenDoc struct {
	User      string    `bson:"user_id"`
	Room      int64     `bson:"room_id"`
	Thread    int64     `bson:"thread_root"`
	Seq       int64     `bson:"seq"`
	CreatedAt time.Time `bson:"created_at,omitempty"`
}

func (h *Hidden) Hide(ctx context.Context, user string, key store.MsgKey, at time.Time) (bool, error) {
	if err := store.ValidateMarkTime(at); err != nil {
		return false, err
	}
	doc, err := encodeHidden(user, key)
	if err != nil {
		return false, err
	}
	filter := bson.D{{Key: "user_id", Value: doc.User}, {Key: "room_id", Value: doc.Room}, {Key: "thread_root", Value: doc.Thread}, {Key: "seq", Value: doc.Seq}}
	update := bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "created_at", Value: at}}}}
	res, err := h.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	switch {
	case mongo.IsDuplicateKeyError(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("hide %d/%d/%d for %q: %w", key.Room, key.Thread, key.Seq, user, err)
	}
	return res.UpsertedCount > 0, nil
}

func (h *Hidden) Get(ctx context.Context, user string, key store.MsgKey) (domain.HiddenMessage, bool, error) {
	doc, err := encodeHidden(user, key)
	if err != nil {
		return domain.HiddenMessage{}, false, err
	}
	filter := bson.D{{Key: "user_id", Value: doc.User}, {Key: "room_id", Value: doc.Room}, {Key: "thread_root", Value: doc.Thread}, {Key: "seq", Value: doc.Seq}}
	var got hiddenDoc
	err = h.coll.FindOne(ctx, filter).Decode(&got)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return domain.HiddenMessage{}, false, nil
	case err != nil:
		return domain.HiddenMessage{}, false, fmt.Errorf("hidden %d/%d/%d of %q: %w", key.Room, key.Thread, key.Seq, user, err)
	}
	out, err := decodeHidden(got)
	return out, err == nil, err
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

func decodeHidden(d hiddenDoc) (domain.HiddenMessage, error) {
	room, roomErr := toUint64("hidden room", d.Room)
	thread, threadErr := toUint64("hidden thread", d.Thread)
	seq, seqErr := toUint64("hidden seq", d.Seq)
	if err := firstErr(roomErr, threadErr, seqErr); err != nil {
		return domain.HiddenMessage{}, err
	}
	return domain.HiddenMessage{User: d.User, Room: room, Thread: thread, Seq: seq, At: d.CreatedAt}, nil
}

func (h *Hidden) HiddenIn(ctx context.Context, user string, room, thread, from, to uint64) ([]uint64, error) {
	r, roomErr := toInt64("room id", room)
	th, threadErr := toInt64("thread", thread)
	lo, fromErr := toInt64("from", from)
	hi, _ := toInt64("to", min(to, math.MaxInt64))
	if roomErr != nil || threadErr != nil || fromErr != nil || from > to {
		return nil, ctx.Err()
	}
	filter := bson.D{
		{Key: "user_id", Value: user}, {Key: "room_id", Value: r}, {Key: "thread_root", Value: th},
		{Key: "seq", Value: bson.D{{Key: "$gte", Value: lo}, {Key: "$lte", Value: hi}}},
	}
	opts := options.Find().SetSort(bson.D{{Key: "seq", Value: 1}}).SetProjection(bson.D{{Key: "seq", Value: 1}, {Key: "_id", Value: 0}})
	docs, err := h.find(ctx, filter, opts)
	if err != nil {
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

func (h *Hidden) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.HiddenMessage, error) {
	if err := store.ValidateLimit(limit, store.MaxHiddenScan); err != nil {
		return nil, err
	}
	r, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "room_id", Value: r}, {Key: "created_at", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	sort := bson.D{{Key: "created_at", Value: 1}, {Key: "user_id", Value: 1}, {Key: "thread_root", Value: 1}, {Key: "seq", Value: 1}}
	docs, err := h.find(ctx, filter, options.Find().SetSort(sort).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("hidden of room %d between %v and %v: %w", room, from, to, err)
	}
	out := make([]domain.HiddenMessage, len(docs))
	for i, d := range docs {
		if out[i], err = decodeHidden(d); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (h *Hidden) find(ctx context.Context, filter bson.D, opts options.Lister[options.FindOptions]) ([]hiddenDoc, error) {
	cur, err := h.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var docs []hiddenDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}
