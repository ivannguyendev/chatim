package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type Mentions struct {
	coll *mongo.Collection
}

func (m *Mentions) ApplyMentions(ctx context.Context, set store.MentionSet) error {
	if err := store.ValidateMentionSet(set); err != nil {
		return err
	}
	room, err := toInt64("room id", set.Key.Room)
	if err != nil {
		return err
	}
	stored, err := m.MentionsOf(ctx, set.Key)
	if err != nil {
		return err
	}
	plan := store.PlanMentions(stored, set)
	models := make([]mongo.WriteModel, 0, len(plan.Live)+len(plan.Retire))
	for _, t := range plan.Live {
		models = append(models, mongo.NewUpdateOneModel().SetFilter(notNewerThan(set, t)).
			SetUpdate(bson.D{{Key: "$set", Value: liveMentionFields(set, t, room)}}).SetUpsert(true))
	}
	for _, t := range plan.Retire {
		models = append(models, mongo.NewUpdateOneModel().SetFilter(notNewerThan(set, t)).
			SetUpdate(bson.D{{Key: "$set", Value: retiredMentionFields(set)}}))
	}
	if len(models) == 0 {
		return nil
	}
	_, err = m.coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	switch {
	case onlyDuplicateKeys(err):
		return fmt.Errorf("apply mentions v%d of %d/%d/%d: a newer version landed: %w", set.Ver, set.Key.Room, set.Key.Thread, set.Key.Seq, domain.ErrRetryLater)
	case err != nil:
		return fmt.Errorf("apply mentions v%d of %d/%d/%d: %w", set.Ver, set.Key.Room, set.Key.Thread, set.Key.Seq, err)
	}
	return nil
}

func (m *Mentions) MentionsOf(ctx context.Context, key store.MsgKey) ([]domain.Mention, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	cur, err := m.coll.Find(ctx, bson.D{{Key: "message_key", Value: msgID(key)}})
	if err != nil {
		return nil, fmt.Errorf("mentions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	var docs []mentionLinkDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mentions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
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

func notNewerThan(set store.MentionSet, t domain.MentionTarget) bson.D {
	return bson.D{
		{Key: "_id", Value: mentionID(set.Key, t)},
		{Key: "message_ver", Value: bson.D{{Key: "$lte", Value: int64(set.Ver)}}},
	}
}
