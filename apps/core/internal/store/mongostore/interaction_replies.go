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

func (r *Interactions) AddReply(ctx context.Context, x domain.Reply) (bool, error) {
	fields, err := replyFields(x)
	if err != nil {
		return false, err
	}
	fields = append(fields,
		bson.E{Key: "state", Value: interactionLive},
		bson.E{Key: "ver", Value: int64(1)},
		bson.E{Key: "created_at", Value: x.At},
		bson.E{Key: "updated_at", Value: x.At},
	)
	res, err := r.coll.UpdateOne(ctx, idIs(replyIDOf(x)), bson.D{{Key: "$setOnInsert", Value: fields}}, options.UpdateOne().SetUpsert(true))
	switch {
	case mongo.IsDuplicateKeyError(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("add reply %d to %d/%d/%d: %w", x.Seq, x.Parent.Room, x.Parent.Thread, x.Parent.Seq, err)
	}
	return res.UpsertedCount > 0, nil
}

func (r *Interactions) RemoveReply(ctx context.Context, x domain.Reply, at time.Time) (bool, error) {
	fields, err := replyFields(x)
	if err != nil {
		return false, err
	}
	if err := store.ValidateMarkTime(at); err != nil {
		return false, err
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.Before).SetProjection(bson.D{{Key: "state", Value: 1}})
	var before struct {
		State int64 `bson:"state"`
	}
	err = r.coll.FindOneAndUpdate(ctx, idIs(replyIDOf(x)), removeReply(fields, at), opts).Decode(&before)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("remove reply %d from %d/%d/%d: %w", x.Seq, x.Parent.Room, x.Parent.Thread, x.Parent.Seq, err)
	}
	return before.State == interactionLive, nil
}

func replyFields(x domain.Reply) (bson.D, error) {
	if err := store.ValidateReply(x); err != nil {
		return nil, err
	}
	room, err := toInt64("room id", x.Room)
	if err != nil {
		return nil, err
	}
	seq, err := toInt64("reply seq", x.Seq)
	if err != nil {
		return nil, err
	}
	fields := interactionFields(store.MsgKey(x.Parent), room, x.Tenant, keys.ReplyKind, x.From)
	return append(fields, bson.E{Key: "reply_seq", Value: seq}), nil
}

func replyIDOf(x domain.Reply) []byte { return replyID(store.MsgKey(x.Parent), store.ReplyKeyOf(x)) }

func (r *Interactions) Replies(ctx context.Context, parent store.MsgKey, afterSeq uint64, limit int) ([]domain.Reply, error) {
	if err := parent.Validate(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxPageLimit); err != nil {
		return nil, err
	}
	lo := keys.InteractionReply(msgID(parent), 0, afterSeq)
	_, hi := keys.InteractionReplyRange(msgID(parent))
	filter := bson.D{{Key: "_id", Value: bson.D{{Key: "$gt", Value: lo}, {Key: "$lt", Value: hi}}}, {Key: "state", Value: interactionLive}}
	docs, err := r.find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("replies of %d/%d/%d after %d: %w", parent.Room, parent.Thread, parent.Seq, afterSeq, err)
	}
	return decodeAll(docs, decodeReply)
}

func replyID(parent, reply store.MsgKey) []byte {
	return keys.InteractionReply(msgID(parent), reply.Thread, reply.Seq)
}
