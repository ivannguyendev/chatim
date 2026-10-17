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
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type ReplyThreads struct {
	coll *mongo.Collection
}

type replyThreadDoc struct {
	ID           []byte    `bson:"_id"`
	Room         int64     `bson:"room_id"`
	Tenant       string    `bson:"tenant"`
	LastReplyAt  time.Time `bson:"last_reply_at"`
	LastReplySeq int64     `bson:"last_reply_seq"`
	UpdatedAt    time.Time `bson:"updated_at"`
}

var replyThreadsByRecent = bson.D{{Key: "room_id", Value: 1}, {Key: "last_reply_at", Value: -1}, {Key: "_id", Value: -1}}

func (r *ReplyThreads) Touch(ctx context.Context, parent store.MsgKey, tenant string, replySeq uint64, at time.Time) error {
	if err := store.ValidateReplyThreadTouch(parent, tenant, replySeq, at); err != nil {
		return err
	}
	room, roomErr := toInt64("room id", parent.Room)
	seq, seqErr := toInt64("reply seq", replySeq)
	if err := errors.Join(roomErr, seqErr); err != nil {
		return err
	}
	at = time.UnixMilli(at.UnixMilli()).UTC()
	opts := options.UpdateOne().SetUpsert(true)
	if _, err := r.coll.UpdateOne(ctx, idIs(msgID(parent)), touchPipeline(room, tenant, seq, at), opts); err != nil {
		return fmt.Errorf("touch reply thread %d/%d/%d with seq %d: %w", parent.Room, parent.Thread, parent.Seq, replySeq, err)
	}
	return nil
}

func touchPipeline(room int64, tenant string, seq int64, at time.Time) mongo.Pipeline {
	newer := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "$gt", Value: bson.A{at, "$last_reply_at"}}},
		bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{at, "$last_reply_at"}}},
			bson.D{{Key: "$gt", Value: bson.A{seq, "$last_reply_seq"}}},
		}}},
	}}}
	pick := func(field string, next any) bson.E {
		return bson.E{Key: field, Value: bson.D{{Key: "$cond", Value: bson.A{newer, next, "$" + field}}}}
	}
	set := bson.D{
		{Key: "room_id", Value: room},
		{Key: "tenant", Value: literal(tenant)},
		pick("last_reply_at", at),
		pick("last_reply_seq", seq),
		pick("updated_at", "$$NOW"),
	}
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func (r *ReplyThreads) List(ctx context.Context, room uint64, before store.ReplyThreadCursor, limit int) ([]store.ReplyThread, error) {
	if err := store.ValidateReplyThreadList(room, before, limit); err != nil {
		return nil, err
	}
	id, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	opts := options.Find().SetSort(bson.D{{Key: "last_reply_at", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(limit)).SetHint(replyThreadsByRecent)
	cur, err := r.coll.Find(ctx, replyThreadFilter(id, room, before), opts)
	if err != nil {
		return nil, fmt.Errorf("reply threads of room %d: %w", room, err)
	}
	var docs []replyThreadDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("reply threads of room %d: %w", room, err)
	}
	out := make([]store.ReplyThread, len(docs))
	for i, d := range docs {
		if out[i], err = decodeReplyThread(d); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func replyThreadFilter(id int64, room uint64, before store.ReplyThreadCursor) bson.D {
	filter := bson.D{
		{Key: "room_id", Value: id},
		{Key: "_id", Value: bson.D{{Key: "$gte", Value: keys.Msg(room, 0, 0)}, {Key: "$lt", Value: keys.Msg(room+1, 0, 0)}}},
	}
	if before.At.IsZero() {
		return filter
	}
	older := bson.D{{Key: "last_reply_at", Value: bson.D{{Key: "$lt", Value: before.At}}}}
	tied := bson.D{{Key: "last_reply_at", Value: before.At}, {Key: "_id", Value: bson.D{{Key: "$lt", Value: keys.Msg(room, 0, before.Seq)}}}}
	return append(filter,
		bson.E{Key: "last_reply_at", Value: bson.D{{Key: "$lte", Value: before.At}}},
		bson.E{Key: "$or", Value: bson.A{older, tied}},
	)
}

func decodeReplyThread(d replyThreadDoc) (store.ReplyThread, error) {
	room, thread, seq, err := keys.ParseMsg(d.ID)
	stored, roomErr := toUint64("reply thread room", d.Room)
	if err != nil || roomErr != nil || room != stored {
		return store.ReplyThread{}, fmt.Errorf("%w: reply thread _id %x", errCorrupt, d.ID)
	}
	last, err := toUint64("reply thread last seq", d.LastReplySeq)
	if err != nil {
		return store.ReplyThread{}, err
	}
	return store.ReplyThread{
		Parent: store.MsgKey{Room: room, Thread: thread, Seq: seq}, Tenant: d.Tenant,
		LastReplyAt: d.LastReplyAt, LastReplySeq: last, UpdatedAt: d.UpdatedAt,
	}, nil
}
