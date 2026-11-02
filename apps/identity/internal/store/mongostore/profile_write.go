package mongostore

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

func (s *Store) WriteProfile(ctx context.Context, w store.ProfileWrite) (bool, error) {
	if w.Seen == nil {
		return s.insertProfile(ctx, w)
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

func (s *Store) insertProfile(ctx context.Context, w store.ProfileWrite) (bool, error) {
	_, err := s.db.Collection(usersCollection).InsertOne(ctx, bson.D{
		{Key: "_id", Value: tenantKey(w.TenantID, w.UserID)},
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
	})
	switch {
	case err == nil:
		return true, nil
	case mongo.IsDuplicateKeyError(err):
		return false, nil
	default:
		return false, s.mapErr(err)
	}
}
