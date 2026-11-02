package mongostore

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const keySeparator = "│"

func tenantKey(tenantID, id string) string {
	return tenantID + keySeparator + id
}

func (s *Store) CreateTenant(ctx context.Context, t store.Tenant) error {
	_, err := s.db.Collection(tenantsCollection).InsertOne(ctx, bson.D{
		{Key: "_id", Value: t.ID},
		{Key: "name", Value: t.Name},
		{Key: "state", Value: t.State},
		{Key: "ver", Value: int64(1)},
		{Key: "group_tree_ver", Value: int64(0)},
		{Key: "created_at", Value: t.CreatedAt},
		{Key: "updated_at", Value: t.UpdatedAt},
		{Key: "updated_by", Value: t.UpdatedBy},
	})
	return s.mapErr(err)
}

func (s *Store) SetTenantState(ctx context.Context, id string, state store.State, r store.Revocation) error {
	changed := differs("state", state)
	set := versionedSet(changed, bson.D{{Key: "state", Value: literal(state)}}, r.At, r.By)
	if state == store.StateDisabled {
		revokes := bson.D{{Key: "$eq", Value: bson.A{"$state", literal(store.StateActive)}}}
		set = append(set, bson.E{Key: "sessions_revoked_at", Value: when(revokes, "$$NOW", "$sessions_revoked_at")})
	}
	return s.updateExisting(ctx, tenantsCollection, id, set)
}

func (s *Store) SetUserState(ctx context.Context, tenantID, id string, state store.State, at time.Time, by string) error {
	set := versionedSet(differs("state", state), bson.D{{Key: "state", Value: literal(state)}}, at, by)
	return s.updateExisting(ctx, usersCollection, tenantKey(tenantID, id), set)
}

func (s *Store) SetProfile(ctx context.Context, tenantID, userID string, u store.ProfileUpdate, at time.Time, by string) error {
	var values bson.D
	changes := bson.A{}
	for _, f := range []struct {
		name  string
		value *string
	}{{"display_name", u.DisplayName}, {"avatar_url", u.AvatarURL}} {
		if f.value == nil {
			continue
		}
		values = append(values, bson.E{Key: f.name, Value: literal(*f.value)})
		changes = append(changes, differs(f.name, *f.value))
	}
	set := versionedSet(bson.D{{Key: "$or", Value: append(changes, false)}}, values, at, by)
	return s.updateExisting(ctx, usersCollection, tenantKey(tenantID, userID), set)
}

func (s *Store) updateExisting(ctx context.Context, collection, id string, set bson.D) error {
	res, err := s.db.Collection(collection).UpdateOne(ctx, bson.D{{Key: "_id", Value: id}}, mongo.Pipeline{{{Key: "$set", Value: set}}})
	if err != nil {
		return s.mapErr(err)
	}
	if res.MatchedCount == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

func versionedSet(changed bson.D, values bson.D, at time.Time, by string) bson.D {
	set := append(bson.D{}, values...)
	return append(set,
		bson.E{Key: "ver", Value: when(changed, bson.D{{Key: "$add", Value: bson.A{"$ver", int64(1)}}}, "$ver")},
		bson.E{Key: "updated_at", Value: when(changed, literal(at), "$updated_at")},
		bson.E{Key: "updated_by", Value: when(changed, literal(by), "$updated_by")},
	)
}

func differs(field string, value any) bson.D {
	return bson.D{{Key: "$ne", Value: bson.A{"$" + field, literal(value)}}}
}

func when(condition bson.D, then, otherwise any) bson.D {
	return bson.D{{Key: "$cond", Value: bson.A{condition, then, otherwise}}}
}

func literal(value any) bson.D {
	return bson.D{{Key: "$literal", Value: value}}
}
