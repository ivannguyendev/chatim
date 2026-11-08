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
	"github.com/ivannguyendev/chatim/pkg/core/mongobound"
)

const getManyBatch = 200

var _ store.Uploads = (*Uploads)(nil)

var locationFields = bson.D{
	{Key: "tenant_id", Value: 1}, {Key: "user_id", Value: 1}, {Key: "content_type", Value: 1}, {Key: "size", Value: 1},
	{Key: "state", Value: 1}, {Key: "ver", Value: 1}, {Key: "created_at", Value: 1},
	{Key: "bucket", Value: 1}, {Key: "attached_at", Value: 1}, {Key: "retired_at", Value: 1},
}

func (s *Uploads) GetMany(ctx context.Context, tenant string, keys []string) (map[string]domain.Upload, error) {
	unique := slices.Compact(slices.Sorted(slices.Values(keys)))
	out := make(map[string]domain.Upload, len(unique))
	for batch := range slices.Chunk(unique, getManyBatch) {
		cur, err := s.coll.Find(ctx, manyFilter(tenant, batch), options.Find().SetProjection(locationFields))
		if err != nil {
			return nil, fmt.Errorf("get uploads: %w", err)
		}
		var docs []uploadDoc
		if err := cur.All(ctx, &docs); err != nil {
			return nil, fmt.Errorf("get uploads: %w", err)
		}
		for _, d := range docs {
			out[d.Key] = fromDoc(d)
		}
	}
	return out, nil
}

func manyFilter(tenant string, keys []string) bson.D {
	return mongobound.StringIDs(keys, bson.E{Key: "tenant_id", Value: tenant})
}

func (s *Uploads) MarkMoved(ctx context.Context, key string, ver int64, at time.Time) error {
	set := bson.D{
		{Key: "bucket", Value: bson.D{{Key: "$literal", Value: string(domain.BucketCompleted)}}},
		{Key: "attached_at", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$attached_at", at}}}},
		{Key: "ver", Value: bson.D{{Key: "$add", Value: bson.A{"$ver", int64(1)}}}},
		{Key: "updated_at", Value: at},
	}
	res, err := s.coll.UpdateOne(ctx, bson.D{{Key: "_id", Value: key}, {Key: "ver", Value: ver}}, mongo.Pipeline{{{Key: "$set", Value: set}}})
	switch {
	case err != nil:
		return fmt.Errorf("mark upload moved: %w", err)
	case res.MatchedCount == 0:
		return domain.ErrRetryLater
	}
	return nil
}

func (s *Uploads) Retire(ctx context.Context, keys []string, at time.Time) error {
	if len(keys) == 0 {
		return nil
	}
	filter := retireFilter(keys)
	if _, err := s.coll.UpdateMany(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "retired_at", Value: at}}}}); err != nil {
		return fmt.Errorf("retire uploads: %w", err)
	}
	return nil
}

func retireFilter(keys []string) bson.D {
	return mongobound.StringIDs(keys, bson.E{Key: "retired_at", Value: bson.D{{Key: "$exists", Value: false}}})
}

func (s *Uploads) Unretire(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	filter := mongobound.StringIDs(keys, bson.E{Key: "retired_at", Value: bson.D{{Key: "$exists", Value: true}}})
	if _, err := s.coll.UpdateMany(ctx, filter, bson.D{{Key: "$unset", Value: bson.D{{Key: "retired_at", Value: ""}}}}); err != nil {
		return fmt.Errorf("unretire uploads: %w", err)
	}
	return nil
}
