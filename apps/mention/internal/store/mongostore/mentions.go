package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

var _ store.Mentions = (*Mentions)(nil)

type Mentions struct {
	coll *mongo.Collection
}

func NewMentions(db *mongo.Database) *Mentions {
	return &Mentions{coll: db.Collection(mentionsCollection)}
}

func (m *Mentions) Apply(ctx context.Context, plan domain.Plan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	models, err := mentionModels(plan, time.Now().UTC())
	if err != nil || len(models) == 0 {
		return err
	}
	if _, err := m.coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
		return fmt.Errorf("apply mentions v%d of %d/%d/%d: %w", plan.Ver, plan.Key.Room, plan.Key.Thread, plan.Key.Seq, err)
	}
	return nil
}

func mentionModels(plan domain.Plan, now time.Time) ([]mongo.WriteModel, error) {
	at, err := placeOf(plan.Key)
	if err != nil {
		return nil, err
	}
	models := make([]mongo.WriteModel, 0, len(plan.Live)+len(plan.Retired))
	add := func(targets []domain.Target, state domain.State) {
		for _, t := range targets {
			models = append(models, mongo.NewUpdateOneModel().
				SetFilter(bson.D{{Key: "_id", Value: mentionID(at, t)}}).
				SetUpdate(mentionUpdate(plan, at, t, state, now)).
				SetUpsert(true))
		}
	}
	add(plan.Live, domain.StateLive)
	add(plan.Retired, domain.StateRetired)
	return models, nil
}

func (m *Mentions) Of(ctx context.Context, tenant string, key domain.MessageKey) ([]domain.Mention, error) {
	if err := store.ValidateMentionsOf(tenant, key); err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "message_key", Value: messageID(key)}, {Key: "tenant", Value: tenant}}
	docs, err := findAll[mentionDoc](ctx, m.coll, filter)
	if err != nil {
		return nil, fmt.Errorf("mentions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	return decodeMentions(docs)
}

func decodeMentions(docs []mentionDoc) ([]domain.Mention, error) {
	out := make([]domain.Mention, 0, len(docs))
	for _, d := range docs {
		got, err := decodeMention(d)
		if err != nil {
			return nil, err
		}
		out = append(out, got)
	}
	return out, nil
}

func findAll[T any](ctx context.Context, coll *mongo.Collection, filter bson.D, opts ...options.Lister[options.FindOptions]) ([]T, error) {
	cur, err := coll.Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	var docs []T
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}
