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

type witnessDoc struct {
	User string `bson:"user_id"`
	N    int64  `bson:"ver"`
}

type countRow struct {
	Emoji string `bson:"_id"`
	N     int64  `bson:"n"`
}

func (r *Reactions) Count(ctx context.Context, key store.MsgKey, witnesses []store.Witness) ([]domain.ReactionCount, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	sess, err := r.client.StartSession(options.Session().SetCausalConsistency(true))
	if err != nil {
		return nil, fmt.Errorf("count reactions of %d/%d/%d: start session: %w", key.Room, key.Thread, key.Seq, err)
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	sctx := mongo.NewSessionContext(ctx, sess)
	if err := r.witnessed(sctx, key, witnesses); err != nil {
		return nil, err
	}
	return r.live(sctx, key)
}

func (r *Reactions) witnessed(ctx context.Context, key store.MsgKey, witnesses []store.Witness) error {
	if len(witnesses) == 0 {
		return nil
	}
	need := make(map[string]uint32, len(witnesses))
	ids := make(bson.A, 0, len(witnesses))
	for _, w := range witnesses {
		if _, seen := need[w.User]; !seen {
			ids = append(ids, reactionID(key, w.User))
		}
		need[w.User] = max(need[w.User], w.N)
	}
	opts := options.Find().SetProjection(bson.D{{Key: "user_id", Value: 1}, {Key: "ver", Value: 1}})
	cur, err := r.coll.Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, opts)
	if err != nil {
		return fmt.Errorf("read reaction witnesses of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	var docs []witnessDoc
	if err := cur.All(ctx, &docs); err != nil {
		return fmt.Errorf("read reaction witnesses of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	seen := 0
	for _, d := range docs {
		if n, ok := need[d.User]; ok && d.N >= int64(n) {
			seen++
		}
	}
	if seen < len(need) {
		return fmt.Errorf("reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, store.ErrStaleRead)
	}
	return nil
}

func countPipeline(key store.MsgKey) mongo.Pipeline {
	match := bson.D{{Key: "message_key", Value: keys.Msg(key.Room, key.Thread, key.Seq)}, {Key: "emoji", Value: bson.D{{Key: "$gt", Value: ""}}}}
	group := bson.D{{Key: "_id", Value: "$emoji"}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}
	return mongo.Pipeline{{{Key: "$match", Value: match}}, {{Key: "$group", Value: group}}}
}

func (r *Reactions) live(ctx context.Context, key store.MsgKey) ([]domain.ReactionCount, error) {
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

func (r *Reactions) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Reaction, error) {
	if err := store.ValidateLimit(limit, store.MaxReactionScan); err != nil {
		return nil, err
	}
	rid, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "room_id", Value: rid}, {Key: "updated_at", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit))
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("reactions of room %d between %v and %v: %w", room, from, to, err)
	}
	var docs []reactionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("reactions of room %d between %v and %v: %w", room, from, to, err)
	}
	return decodeReactions(docs)
}
