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

type witnessDoc struct {
	ID []byte `bson:"_id"`
	N  int64  `bson:"ver"`
}

func (r *Interactions) CountWitnessed(ctx context.Context, key store.MsgKey, witnesses []store.Witness) ([]domain.ReactionCount, error) {
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
	return r.countLive(sctx, key)
}

func (r *Interactions) witnessed(ctx context.Context, key store.MsgKey, witnesses []store.Witness) error {
	if len(witnesses) == 0 {
		return nil
	}
	need := make(map[string]uint32, len(witnesses))
	ids := make(bson.A, 0, len(witnesses))
	for _, w := range witnesses {
		id := userID(key, keys.ReactionKind, w.User)
		if _, seen := need[string(id)]; !seen {
			ids = append(ids, id)
		}
		need[string(id)] = max(need[string(id)], w.N)
	}
	opts := options.Find().SetProjection(bson.D{{Key: "ver", Value: 1}})
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
		if n, ok := need[string(d.ID)]; ok && d.N >= int64(n) {
			seen++
		}
	}
	if seen < len(need) {
		return fmt.Errorf("reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, store.ErrStaleRead)
	}
	return nil
}
