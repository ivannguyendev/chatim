package mongostore

import (
	"context"
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
	if err := store.ValidateReply(x); err != nil {
		return false, err
	}
	room, err := toInt64("room id", x.Room)
	if err != nil {
		return false, err
	}
	seq, err := toInt64("reply seq", x.Seq)
	if err != nil {
		return false, err
	}
	parent := store.MsgKey(x.Parent)
	insert := append(interactionFields(parent, room, x.Tenant, keys.ReplyKind, x.From),
		bson.E{Key: "reply_seq", Value: seq},
		bson.E{Key: "state", Value: interactionLive},
		bson.E{Key: "ver", Value: int64(1)},
		bson.E{Key: "created_at", Value: x.At},
		bson.E{Key: "updated_at", Value: x.At},
	)
	id := replyID(parent, store.ReplyKeyOf(x))
	res, err := r.coll.UpdateOne(ctx, idIs(id), bson.D{{Key: "$setOnInsert", Value: insert}}, options.UpdateOne().SetUpsert(true))
	switch {
	case mongo.IsDuplicateKeyError(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("add reply %d/%d to %d/%d/%d: %w", x.Thread, x.Seq, parent.Room, parent.Thread, parent.Seq, err)
	}
	return res.UpsertedCount > 0, nil
}

func (r *Interactions) RemoveReply(ctx context.Context, parent, reply store.MsgKey, at time.Time) (bool, error) {
	if err := store.ValidateReplyTarget(parent, reply); err != nil {
		return false, err
	}
	filter := bson.D{{Key: "_id", Value: replyID(parent, reply)}, {Key: "state", Value: interactionLive}}
	res, err := r.coll.UpdateOne(ctx, filter, removeInteraction(at, false))
	if err != nil {
		return false, fmt.Errorf("remove reply %d/%d from %d/%d/%d: %w", reply.Thread, reply.Seq, parent.Room, parent.Thread, parent.Seq, err)
	}
	return res.ModifiedCount == 1, nil
}

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
