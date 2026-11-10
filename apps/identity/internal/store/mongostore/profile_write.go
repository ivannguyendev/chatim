package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/core/redact"
)

var errProfileWithoutSeen = fmt.Errorf("%w: a profile write needs the seen values", apperr.ErrInvalidArgument)

func (s *Store) WriteProfile(ctx context.Context, w store.ProfileWrite) (bool, error) {
	if w.Seen == nil {
		return false, errProfileWithoutSeen
	}
	filter := bson.D{
		{Key: "_id", Value: tenantKey(w.TenantID, w.UserID)},
		{Key: "display_name", Value: w.Seen.DisplayName},
		{Key: "avatar_url", Value: w.Seen.AvatarURL},
	}
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "display_name", Value: w.DisplayName},
			{Key: "avatar_url", Value: w.AvatarURL},
			{Key: "updated_at", Value: w.At},
			{Key: "updated_by", Value: w.By},
		}},
		{Key: "$inc", Value: bson.D{{Key: "ver", Value: int64(1)}}},
	}
	res, err := s.db.Collection(usersCollection).UpdateOne(ctx, filter, update)
	if err != nil {
		return false, s.mapErr(err)
	}
	return res.MatchedCount > 0, nil
}

func newUserDoc(key string, w store.ProfileWrite) bson.D {
	return bson.D{
		{Key: "_id", Value: key},
		{Key: "tenant_id", Value: w.TenantID},
		{Key: "id", Value: w.UserID},
		{Key: "display_name", Value: w.DisplayName},
		{Key: "avatar_url", Value: w.AvatarURL},
		{Key: "state", Value: store.StateActive},
		{Key: "groups", Value: bson.A{}},
		{Key: "ver", Value: int64(1)},
		{Key: "created_at", Value: w.At},
		{Key: "updated_at", Value: w.At},
		{Key: "updated_by", Value: w.By},
	}
}

func (s *Store) insertMany(collection string) insertManyFunc {
	return func(ctx context.Context, docs []any) error {
		_, err := s.db.Collection(collection).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
		return err
	}
}

func (s *Store) unavailable(err error) error {
	return fmt.Errorf("%w: %w", apperr.ErrUnavailable, redact.Error(err, s.uri))
}
