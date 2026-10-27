package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const (
	lanID   = "a1aaaaaaaaaaaaaaaaaaaaaaaa"
	phoneID = "s1aaaaaaaaaaaaaaaaaaaaaaaa"
	laptop  = "s2aaaaaaaaaaaaaaaaaaaaaaaa"
)

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

func wantRevoked(t *testing.T, s fixture, ids ...string) {
	t.Helper()
	got := revokedRows(t, s)
	if len(got) != len(ids) {
		t.Fatalf("revoked_sessions = %v; want %v", got, ids)
	}
	for _, id := range ids {
		keep, ok := got[id]
		if !ok || keep != testKeep {
			t.Fatalf("revoked_sessions = %v; want %s kept %v", got, id, testKeep)
		}
	}
}

func TestEveryRevokePathRecordsItsSessions(t *testing.T) {
	ctx := context.Background()
	paths := map[string]func(fixture, store.Account) error{
		"account disable": func(s fixture, a store.Account) error {
			return s.SetAccountState(ctx, "shop", a.ID, store.StateDisabled, revocation(), audit("account_disable"))
		},
		"password change": func(s fixture, a store.Account) error {
			return s.SetPassword(ctx, store.PasswordChange{TenantID: "shop", AccountID: a.ID, ExpectVer: a.Ver, Hash: a.PasswordHash, Revocation: revocation()}, audit("password_change"))
		},
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			s := openStore(t)
			seedTenant(t, s, "shop")
			a := seedAccount(t, s, "shop", lanID, "lan")
			seedSessions(t, s, a, phoneID, laptop)
			if err := run(s, a); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			wantRevoked(t, s, phoneID, laptop)
		})
	}
}

func TestRevokeSessionRecordsOnceAndReportsTheRevoke(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", lanID, "lan")
	seedSessions(t, s, a, phoneID, laptop)
	revoked, err := s.RevokeSession(ctx, phoneID, revocation(), audit("session_revoke"))
	if err != nil || !revoked {
		t.Fatalf("RevokeSession = %v, %v; want true", revoked, err)
	}
	wantRevoked(t, s, phoneID)
	again, err := s.RevokeSession(ctx, phoneID, revocation(), audit("session_revoke"))
	if err != nil || again {
		t.Fatalf("second RevokeSession = %v, %v; want false", again, err)
	}
	wantRevoked(t, s, phoneID)
}

func TestTenantDisableRecordsOneRevokeTime(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", lanID, "lan")
	seedSessions(t, s, a, phoneID)
	if err := s.SetTenantState(ctx, "shop", store.StateDisabled, revocation(), audit("tenant_disable")); err != nil {
		t.Fatalf("disable: %v", err)
	}
	wantRevoked(t, s)
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

func TestTokenIssuedBeforeATenantRevokeIsOlderThanItsTime(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", lanID, "lan")
	seedSessions(t, s, a, phoneID)
	tx, err := s.raw.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	var issued time.Time
	if err := tx.QueryRow(ctx, "UPDATE sessions SET refresh_ver = refresh_ver + 1 WHERE id = $1 RETURNING transaction_timestamp()", phoneID).Scan(&issued); err != nil {
		t.Fatalf("hold the session row: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- s.SetTenantState(ctx, "shop", store.StateDisabled, revocation(), audit("tenant_disable"))
	}()
	waitForLockWait(t, s)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("disable: %v", err)
	}
	snap, err := s.Revocations(ctx)
	if err != nil || len(snap.Tenants) != 1 {
		t.Fatalf("Revocations = %+v, %v", snap, err)
	}
	if !snap.Tenants[0].RevokedBefore.After(issued) {
		t.Fatalf("revoke time %v is not after the refresh issued at %v", snap.Tenants[0].RevokedBefore, issued)
	}
}

func waitForLockWait(t *testing.T, s fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting int
		if err := s.raw.QueryRow(ctx, "SELECT count(*) FROM pg_locks WHERE NOT granted AND locktype = 'transactionid'").Scan(&waiting); err != nil {
			t.Fatalf("pg_locks: %v", err)
		}
		if waiting > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("the tenant disable never waited on the session row")
		case <-tick.C:
		}
	}
}

func TestIssueTimesComeFromTheTransaction(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", lanID, "lan")
	created, err := s.CreateSession(ctx, newSession(phoneID, a), audit("login_ok"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rotated, err := s.RotateRefresh(ctx, store.Rotation{SessionID: phoneID, ExpectVer: 1, RefreshHash: digest("x"), IdleExpiresAt: t0.Add(time.Hour), LastUsedAt: t0}, audit("refresh"))
	if err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}
	var dbNow time.Time
	if err := s.raw.QueryRow(ctx, "SELECT now()").Scan(&dbNow); err != nil {
		t.Fatalf("now: %v", err)
	}
	if created.IsZero() || rotated.Before(created) || rotated.After(dbNow) {
		t.Fatalf("created %v, rotated %v, db now %v", created, rotated, dbNow)
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
