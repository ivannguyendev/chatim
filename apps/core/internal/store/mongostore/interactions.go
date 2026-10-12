package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type Interactions struct {
	coll   *mongo.Collection
	client *mongo.Client
}

func (r *Interactions) SetReaction(ctx context.Context, x domain.Reaction) (domain.Reaction, bool, error) {
	if err := store.ValidateReaction(x); err != nil {
		return domain.Reaction{}, false, err
	}
	if err := domain.ValidateEmoji(x.Emoji); err != nil {
		return domain.Reaction{}, false, err
	}
	key := store.ReactionKeyOf(x)
	room, err := toInt64("room id", x.Room)
	if err != nil {
		return domain.Reaction{}, false, err
	}
	head := interactionFields(key, room, x.Tenant, keys.ReactionKind, x.User)
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.Before)
	var d interactionDoc
	err = r.coll.FindOneAndUpdate(ctx, idIs(userID(key, keys.ReactionKind, x.User)), setReaction(head, x), opts).Decode(&d)
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

func (r *Interactions) RemoveReaction(ctx context.Context, key store.MsgKey, user string, at time.Time) (domain.Reaction, bool, error) {
	if err := store.ValidateReactionTarget(key, user); err != nil {
		return domain.Reaction{}, false, err
	}
	filter := bson.D{{Key: "_id", Value: userID(key, keys.ReactionKind, user)}, {Key: "state", Value: interactionLive}}
	var d interactionDoc
	err := r.coll.FindOneAndUpdate(ctx, filter, removeInteraction(at, true), options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		cur, _, err := r.GetReaction(ctx, key, user)
		return cur, false, err
	case err != nil:
		return domain.Reaction{}, false, fmt.Errorf("remove reaction of %q on %d/%d/%d: %w", user, key.Room, key.Thread, key.Seq, err)
	}
	got, err := decodeReaction(d)
	return got, err == nil, err
}

func (r *Interactions) GetReaction(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error) {
	d, found, err := r.get(ctx, userID(key, keys.ReactionKind, user))
	if err != nil || !found {
		return domain.Reaction{}, false, err
	}
	got, err := decodeReaction(d)
	return got, err == nil, err
}

func (r *Interactions) get(ctx context.Context, id []byte) (interactionDoc, bool, error) {
	var d interactionDoc
	err := r.coll.FindOne(ctx, idIs(id)).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return interactionDoc{}, false, nil
	case err != nil:
		return interactionDoc{}, false, fmt.Errorf("get interaction %x: %w", id, err)
	}
	return d, true, nil
}

func idIs(id []byte) bson.D { return bson.D{{Key: "_id", Value: id}} }

func msgID(key store.MsgKey) []byte { return keys.Msg(key.Room, key.Thread, key.Seq) }

func userID(key store.MsgKey, kind keys.InteractionKind, user string) []byte {
	return keys.InteractionUser(msgID(key), kind, user)
}
