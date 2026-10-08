package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type Reactions struct {
	coll   *mongo.Collection
	client *mongo.Client
}

func (r *Reactions) Set(ctx context.Context, x domain.Reaction) (domain.Reaction, bool, error) {
	if err := store.ValidateReaction(x); err != nil {
		return domain.Reaction{}, false, err
	}
	if err := domain.ValidateEmoji(x.Emoji); err != nil {
		return domain.Reaction{}, false, err
	}
	room, err := toInt64("room id", x.Room)
	if err != nil {
		return domain.Reaction{}, false, err
	}
	filter := bson.D{{Key: "_id", Value: reactionID(store.ReactionKeyOf(x), x.User)}}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.Before)
	var d reactionDoc
	err = r.coll.FindOneAndUpdate(ctx, filter, setReaction(x, room), opts).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return setResult(x, domain.Reaction{}), true, nil
	case err != nil:
		return domain.Reaction{}, false, fmt.Errorf("set reaction of %q on %d/%d/%d: %w", x.User, x.Room, x.Thread, x.Seq, err)
	}
	before, err := decodeReaction(d)
	switch {
	case err != nil:
		return domain.Reaction{}, false, err
	case before.Emoji == x.Emoji:
		return before, false, nil
	}
	return setResult(x, before), true, nil
}

func setResult(x, before domain.Reaction) domain.Reaction {
	x.Prev, x.N, x.At = before.Emoji, before.N+1, time.UnixMilli(x.At.UnixMilli()).UTC()
	return x
}

func (r *Reactions) Remove(ctx context.Context, key store.MsgKey, user string, at time.Time) (domain.Reaction, bool, error) {
	if err := store.ValidateReactionTarget(key, user); err != nil {
		return domain.Reaction{}, false, err
	}
	filter := bson.D{{Key: "_id", Value: reactionID(key, user)}, {Key: "emoji", Value: bson.D{{Key: "$ne", Value: ""}}}}
	var d reactionDoc
	err := r.coll.FindOneAndUpdate(ctx, filter, removeReaction(at), options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		cur, _, err := r.Get(ctx, key, user)
		return cur, false, err
	case err != nil:
		return domain.Reaction{}, false, fmt.Errorf("remove reaction of %q on %d/%d/%d: %w", user, key.Room, key.Thread, key.Seq, err)
	}
	got, err := decodeReaction(d)
	return got, err == nil, err
}

func (r *Reactions) Get(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error) {
	var d reactionDoc
	err := r.coll.FindOne(ctx, bson.D{{Key: "_id", Value: reactionID(key, user)}}).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return domain.Reaction{}, false, nil
	case err != nil:
		return domain.Reaction{}, false, fmt.Errorf("get reaction of %q on %d/%d/%d: %w", user, key.Room, key.Thread, key.Seq, err)
	}
	got, err := decodeReaction(d)
	return got, err == nil, err
}

func reactionID(key store.MsgKey, user string) []byte {
	return keys.Reaction(key.Room, key.Thread, key.Seq, user)
}

func setReaction(x domain.Reaction, room int64) mongo.Pipeline {
	same := bson.D{{Key: "$eq", Value: bson.A{"$emoji", literal(x.Emoji)}}}
	keep := func(field string, next any) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{same, "$" + field, next}}}
	}
	set := bson.D{
		{Key: "message_key", Value: keys.Msg(x.Room, x.Thread, x.Seq)},
		{Key: "room_id", Value: room},
		{Key: "tenant", Value: literal(x.Tenant)},
		{Key: "user_id", Value: literal(x.User)},
		{Key: "previous_emoji", Value: keep("previous_emoji", bson.D{{Key: "$ifNull", Value: bson.A{"$emoji", ""}}})},
		{Key: "emoji", Value: keep("emoji", literal(x.Emoji))},
		{Key: "ver", Value: keep("ver", nextChange())},
		{Key: "updated_at", Value: keep("updated_at", x.At)},
	}
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func removeReaction(at time.Time) mongo.Pipeline {
	set := bson.D{
		{Key: "previous_emoji", Value: "$emoji"},
		{Key: "emoji", Value: ""},
		{Key: "ver", Value: nextChange()},
		{Key: "updated_at", Value: at},
	}
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func nextChange() bson.D {
	return bson.D{{Key: "$add", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$ver", 0}}}, 1}}}
}

func literal(v string) bson.D { return bson.D{{Key: "$literal", Value: v}} }
