package mongostore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
)

var _ store.MessageFiles = (*MessageFiles)(nil)

type MessageFiles struct {
	coll *mongo.Collection
}

func NewMessageFiles(db *mongo.Database) *MessageFiles {
	return &MessageFiles{coll: db.Collection(messageFilesCollection)}
}

func pairID(messageKey, fileKey string) string {
	return messageKey + "|" + fileKey
}

func (s *MessageFiles) Attach(ctx context.Context, pairs []domain.MessageFile) error {
	if len(pairs) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, len(pairs))
	for i, p := range pairs {
		insert := bson.D{
			{Key: "message_key", Value: p.MessageKey}, {Key: "file_key", Value: p.FileKey}, {Key: "tenant_id", Value: p.Tenant},
			{Key: "state", Value: int32(domain.PairLive)}, {Key: "created_at", Value: p.CreatedAt},
		}
		models[i] = mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "_id", Value: pairID(p.MessageKey, p.FileKey)}}).
			SetUpdate(bson.D{{Key: "$setOnInsert", Value: insert}}).
			SetUpsert(true)
	}
	_, err := s.coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	switch {
	case hasCode(err, duplicateKeyCode):
		return domain.ErrRetryLater
	case err != nil:
		return fmt.Errorf("attach message files: %w", err)
	}
	return nil
}

func activeFilter(tenant, messageKey string) bson.D {
	return bson.D{{Key: "tenant_id", Value: tenant}, {Key: "message_key", Value: messageKey}, {Key: "state", Value: int32(domain.PairLive)}}
}

func (s *MessageFiles) Active(ctx context.Context, tenant, messageKey string) ([]string, error) {
	opts := options.Find().SetProjection(bson.D{{Key: "_id", Value: 0}, {Key: "file_key", Value: 1}}).SetHint(filesByMessage)
	cur, err := s.coll.Find(ctx, activeFilter(tenant, messageKey), opts)
	if err != nil {
		return nil, fmt.Errorf("active message files: %w", err)
	}
	var docs []struct {
		FileKey string `bson:"file_key"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("active message files: %w", err)
	}
	keys := make([]string, len(docs))
	for i, d := range docs {
		keys[i] = d.FileKey
	}
	slices.Sort(keys)
	return keys, nil
}

func (s *MessageFiles) Release(ctx context.Context, tenant, messageKey string, at time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "state", Value: int32(domain.PairDeleted)}, {Key: "deleted_at", Value: at}}}}
	if _, err := s.coll.UpdateMany(ctx, activeFilter(tenant, messageKey), update, options.UpdateMany().SetHint(filesByMessage)); err != nil {
		return fmt.Errorf("release message files: %w", err)
	}
	return nil
}

func stillUsedFilter(fileKeys []string) bson.D {
	return bson.D{{Key: "file_key", Value: bson.D{{Key: "$in", Value: fileKeys}}}, {Key: "state", Value: int32(domain.PairLive)}}
}

func (s *MessageFiles) StillUsed(ctx context.Context, fileKeys []string) (map[string]bool, error) {
	used := make(map[string]bool, len(fileKeys))
	if len(fileKeys) == 0 {
		return used, nil
	}
	for _, k := range fileKeys {
		used[k] = false
	}
	var live []string
	if err := s.coll.Distinct(ctx, "file_key", stillUsedFilter(fileKeys)).Decode(&live); err != nil {
		return nil, fmt.Errorf("files still used: %w", err)
	}
	for _, k := range live {
		used[k] = true
	}
	return used, nil
}
