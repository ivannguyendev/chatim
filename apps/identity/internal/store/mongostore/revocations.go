package mongostore

import (
	"cmp"
	"context"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/pkg/auth"
)

type revokedSessionDoc struct {
	ID        string    `bson:"_id"`
	TenantID  string    `bson:"tenant_id"`
	ExpiresAt time.Time `bson:"expires_at"`
}

type revokedTenantDoc struct {
	ID            string    `bson:"_id"`
	RevokedBefore time.Time `bson:"revoked_before"`
	ExpiresAt     time.Time `bson:"expires_at"`
}

func (s *Store) Revocations(ctx context.Context) (auth.RevocationSnapshot, error) {
	var snap auth.RevocationSnapshot
	var sessions []revokedSessionDoc
	live := bson.D{{Key: "$expr", Value: bson.D{{Key: "$gt", Value: bson.A{"$expires_at", "$$NOW"}}}}}
	if err := s.findAll(ctx, revokedSessionsCollection, live, &sessions); err != nil {
		return snap, err
	}
	for _, d := range sessions {
		snap.Sessions = append(snap.Sessions, auth.RevokedSession{ID: d.ID, Tenant: d.TenantID, ExpiresAt: d.ExpiresAt})
	}
	var tenants []revokedTenantDoc
	if err := s.revokedTenants(ctx, &tenants); err != nil {
		return snap, err
	}
	for _, d := range tenants {
		snap.Tenants = append(snap.Tenants, auth.RevokedTenant{ID: d.ID, RevokedBefore: d.RevokedBefore, ExpiresAt: d.ExpiresAt})
	}
	slices.SortFunc(snap.Sessions, func(a, b auth.RevokedSession) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(snap.Tenants, func(a, b auth.RevokedTenant) int { return cmp.Compare(a.ID, b.ID) })
	return snap, nil
}

func (s *Store) revokedTenants(ctx context.Context, out *[]revokedTenantDoc) error {
	expires := bson.D{{Key: "$add", Value: bson.A{"$sessions_revoked_at", s.keep.Milliseconds()}}}
	cur, err := s.db.Collection(tenantsCollection).Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$gt", Value: bson.A{expires, "$$NOW"}}}}}}},
		{{Key: "$project", Value: bson.D{{Key: "revoked_before", Value: "$sessions_revoked_at"}, {Key: "expires_at", Value: expires}}}},
	})
	if err != nil {
		return s.mapErr(err)
	}
	return s.mapErr(cur.All(ctx, out))
}

func (s *Store) DeleteExpiredRevocations(ctx context.Context) (int64, error) {
	expired := bson.D{{Key: "$expr", Value: bson.D{{Key: "$lt", Value: bson.A{"$expires_at", "$$NOW"}}}}}
	res, err := s.db.Collection(revokedSessionsCollection).DeleteMany(ctx, expired)
	if err != nil {
		return 0, s.mapErr(err)
	}
	return res.DeletedCount, nil
}
