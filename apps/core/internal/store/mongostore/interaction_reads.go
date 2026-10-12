package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type countRow struct {
	Emoji string `bson:"_id"`
	N     int64  `bson:"n"`
}

func liveOf(key store.MsgKey, kind keys.InteractionKind) bson.D {
	return bson.D{{Key: "message_key", Value: msgID(key)}, {Key: "kind", Value: interactionKindNames[kind]}, {Key: "state", Value: interactionLive}}
}

func countPipeline(key store.MsgKey) mongo.Pipeline {
	group := bson.D{{Key: "_id", Value: "$value"}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}
	return mongo.Pipeline{{{Key: "$match", Value: liveOf(key, keys.ReactionKind)}}, {{Key: "$group", Value: group}}}
}

func (r *Interactions) CountReactions(ctx context.Context, key store.MsgKey) ([]domain.ReactionCount, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	cur, err := r.coll.Aggregate(ctx, countPipeline(key))
	if err != nil {
		return nil, fmt.Errorf("count reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	var rows []countRow
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("count reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	out := make([]domain.ReactionCount, 0, len(rows))
	for _, row := range rows {
		n, err := narrowUint32("reaction count", row.N)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.ReactionCount{Emoji: row.Emoji, Count: n})
	}
	domain.SortReactionCounts(out)
	return out, nil
}

func (r *Interactions) CountLiveReplies(ctx context.Context, parent store.MsgKey) (uint32, error) {
	if err := parent.Validate(); err != nil {
		return 0, err
	}
	n, err := r.coll.CountDocuments(ctx, liveOf(parent, keys.ReplyKind))
	if err != nil {
		return 0, fmt.Errorf("count replies of %d/%d/%d: %w", parent.Room, parent.Thread, parent.Seq, err)
	}
	return narrowUint32("reply count", n)
}

func (r *Interactions) Between(ctx context.Context, q store.InteractionScan) ([]store.Interaction, error) {
	if err := store.ValidateInteractionScan(q); err != nil {
		return nil, err
	}
	rid, err := toInt64("room id", q.Room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{
		{Key: "room_id", Value: rid}, {Key: "kind", Value: interactionKindNames[q.Kind]},
		{Key: "updated_at", Value: bson.D{{Key: "$gte", Value: q.From}, {Key: "$lte", Value: q.To}}},
	}
	if q.After != nil {
		later := bson.D{{Key: "updated_at", Value: bson.D{{Key: "$gt", Value: q.After.At}}}}
		tied := bson.D{{Key: "updated_at", Value: q.After.At}, {Key: "_id", Value: bson.D{{Key: "$gt", Value: store.InteractionID(*q.After)}}}}
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{later, tied}})
	}
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(q.Limit))
	docs, err := r.find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("interactions of room %d between %v and %v: %w", q.Room, q.From, q.To, err)
	}
	return decodeAll(docs, decodeInteraction)
}

func (r *Interactions) find(ctx context.Context, filter bson.D, opts *options.FindOptionsBuilder) ([]interactionDoc, error) {
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var docs []interactionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}
