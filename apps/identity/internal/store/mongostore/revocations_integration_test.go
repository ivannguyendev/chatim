package mongostore

import (
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func TestRevocationsListDisabledTenantsWithinKeep(t *testing.T) {
	s := testDatabase(t)
	for _, id := range []string{"shop", "bank", "cafe"} {
		mustCreateTenant(t, s, id)
	}
	for _, id := range []string{"shop", "bank"} {
		if err := s.SetTenantState(t.Context(), id, store.StateDisabled, store.Revocation{By: "cli:ops", At: itAt}); err != nil {
			t.Fatalf("disable %s: %v", id, err)
		}
	}
	past := serverNow(t, s).Add(-2 * time.Hour)
	_, err := s.db.Collection(tenantsCollection).UpdateOne(t.Context(), bson.D{{Key: "_id", Value: "bank"}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "sessions_revoked_at", Value: past}}}})
	if err != nil {
		t.Fatalf("move bank revocation back: %v", err)
	}
	mark := readTenant(t, s, "shop").SessionsRevokedAt
	if mark == nil {
		t.Fatal("shop has no sessions_revoked_at")
	}
	snap, err := s.Revocations(t.Context())
	if err != nil {
		t.Fatalf("Revocations: %v", err)
	}
	if len(snap.Tenants) != 1 {
		t.Fatalf("tenants = %+v; want only shop", snap.Tenants)
	}
	got := snap.Tenants[0]
	if got.ID != "shop" || !got.RevokedBefore.Equal(*mark) || !got.ExpiresAt.Equal(mark.Add(time.Hour)) {
		t.Fatalf("tenant = %+v; want shop revoked before %v until %v", got, *mark, mark.Add(time.Hour))
	}
}

func TestRevocationsListLiveSessionsAndDeleteExpiredDropsTheRest(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	now := serverNow(t, s)
	for _, e := range []struct {
		id      string
		expires time.Time
	}{{"s-b", now.Add(time.Hour)}, {"s-a", now.Add(2 * time.Hour)}, {"s-old", now.Add(-time.Minute)}} {
		insertDoc(t, s, revokedSessionsCollection, bson.D{
			{Key: "_id", Value: e.id},
			{Key: "tenant_id", Value: "shop"},
			{Key: "revoked_at", Value: now.Add(-time.Hour)},
			{Key: "expires_at", Value: e.expires},
		})
	}
	snap, err := s.Revocations(t.Context())
	if err != nil {
		t.Fatalf("Revocations: %v", err)
	}
	want := []revocation.Session{
		{ID: "s-a", Tenant: "shop", ExpiresAt: now.Add(2 * time.Hour)},
		{ID: "s-b", Tenant: "shop", ExpiresAt: now.Add(time.Hour)},
	}
	if !slices.EqualFunc(snap.Sessions, want, func(a, b revocation.Session) bool {
		return a.ID == b.ID && a.Tenant == b.Tenant && a.ExpiresAt.Equal(b.ExpiresAt)
	}) {
		t.Fatalf("sessions = %+v; want %+v", snap.Sessions, want)
	}
	if len(snap.Tenants) != 0 {
		t.Fatalf("tenants = %+v; want none", snap.Tenants)
	}
	deleted, err := s.DeleteExpiredRevocations(t.Context())
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteExpiredRevocations = %d, %v; want 1", deleted, err)
	}
	left, err := s.db.Collection(revokedSessionsCollection).CountDocuments(t.Context(), bson.D{})
	if err != nil || left != 2 {
		t.Fatalf("revoked_sessions left = %d, %v; want 2", left, err)
	}
	if again, err := s.DeleteExpiredRevocations(t.Context()); err != nil || again != 0 {
		t.Fatalf("DeleteExpiredRevocations again = %d, %v; want 0", again, err)
	}
}
