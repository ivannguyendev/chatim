package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const lanID = "a1aaaaaaaaaaaaaaaaaaaaaaaa"

func revokedRows(t *testing.T, s fixture) map[string]time.Duration {
	t.Helper()
	rows, err := s.raw.Query(context.Background(), "SELECT session_id, expires_at - revoked_at FROM revoked_sessions")
	if err != nil {
		t.Fatalf("query revoked_sessions: %v", err)
	}
	defer rows.Close()
	out := map[string]time.Duration{}
	for rows.Next() {
		var id string
		var keep time.Duration
		if err := rows.Scan(&id, &keep); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[id] = keep
	}
	return out
}

func TestTenantDisableRecordsOneRevokeTime(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedUser(t, s, "shop", lanID, "lan")
	if err := s.SetTenantState(ctx, "shop", store.StateDisabled, revocation(), audit("tenant_disable")); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if rows := revokedRows(t, s); len(rows) != 0 {
		t.Fatalf("revoked_sessions = %v; want none", rows)
	}
	snap, err := s.Revocations(ctx)
	if err != nil {
		t.Fatalf("Revocations: %v", err)
	}
	if len(snap.Tenants) != 1 || snap.Tenants[0].ID != "shop" || snap.Tenants[0].ExpiresAt.Sub(snap.Tenants[0].RevokedBefore) != testKeep {
		t.Fatalf("tenants = %+v", snap.Tenants)
	}
	first := snap.Tenants[0].RevokedBefore
	if err := s.SetTenantState(ctx, "shop", store.StateActive, revocation(), audit("tenant_enable")); err != nil {
		t.Fatalf("enable: %v", err)
	}
	after, err := s.Revocations(ctx)
	if err != nil || len(after.Tenants) != 1 || !after.Tenants[0].RevokedBefore.Equal(first) {
		t.Fatalf("after enable = %+v, %v; want the revoke time kept", after.Tenants, err)
	}
}

func TestRevocationsListOnlyLiveEntriesAndCleanupDropsTheRest(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	if _, err := s.raw.Exec(ctx, "INSERT INTO revoked_sessions VALUES ('live', 'shop', now(), now() + interval '10 minutes'), ('gone', 'shop', now() - interval '1 hour', now() - interval '1 minute')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.raw.Exec(ctx, "INSERT INTO tenants (id, name, state, ver, created_at, updated_at, updated_by, sessions_revoked_at) VALUES ('old', 'old', 1, 1, now(), now(), 't', now() - interval '1 hour')"); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	snap, err := s.Revocations(ctx)
	if err != nil {
		t.Fatalf("Revocations: %v", err)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].ID != "live" || snap.Sessions[0].Tenant != "shop" || len(snap.Tenants) != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
	deleted, err := s.DeleteExpiredRevocations(ctx)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteExpiredRevocations = %d, %v; want 1", deleted, err)
	}
	if rows := revokedRows(t, s); len(rows) != 1 {
		t.Fatalf("rows after cleanup = %v", rows)
	}
}

func TestOpenRefusesAMissingRevocationKeep(t *testing.T) {
	uri := testDatabaseURI(t)
	if _, err := openWithoutKeep(uri); err == nil {
		t.Fatal("Open accepted a zero revocation keep")
	}
}
