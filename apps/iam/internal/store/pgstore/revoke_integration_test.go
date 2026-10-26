package pgstore_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestDisablingRevokesEverySession(t *testing.T) {
	ctx := context.Background()
	disable := map[string]func(fixture) error{
		"account": func(s fixture) error {
			return s.SetAccountState(ctx, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", store.StateDisabled, revocation(), audit("account_disable"))
		},
		"tenant": func(s fixture) error {
			return s.SetTenantState(ctx, "shop", store.StateDisabled, revocation(), audit("tenant_disable"))
		},
	}
	for name, run := range disable {
		t.Run(name, func(t *testing.T) {
			s := openStore(t)
			seedTenant(t, s, "shop")
			a := seedAccount(t, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
			seedSessions(t, s, a, "s1aaaaaaaaaaaaaaaaaaaaaaaa", "s2aaaaaaaaaaaaaaaaaaaaaaaa")
			if err := run(s); err != nil {
				t.Fatalf("disable %s: %v", name, err)
			}
			for _, id := range []string{"s1aaaaaaaaaaaaaaaaaaaaaaaa", "s2aaaaaaaaaaaaaaaaaaaaaaaa"} {
				got, err := s.SessionByID(ctx, id)
				if err != nil || got.State != store.StateDisabled || got.RevokedReason != "test" {
					t.Fatalf("session %s = %+v, %v; want revoked", id, got, err)
				}
			}
			if live, _ := s.ActiveSessions(ctx, "shop", a.ID); len(live) != 0 {
				t.Fatalf("active sessions = %d, want 0", len(live))
			}
		})
	}
}

func TestRevokedSessionCannotRotateEvenWithTheRightVersion(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
	seedSessions(t, s, a, "s1aaaaaaaaaaaaaaaaaaaaaaaa")
	revoked, err := s.RevokeSession(ctx, "s1aaaaaaaaaaaaaaaaaaaaaaaa", revocation(), audit("session_revoke"))
	if err != nil || !revoked {
		t.Fatalf("RevokeSession = %v, %v; want true, nil", revoked, err)
	}
	again, err := s.RevokeSession(ctx, "s1aaaaaaaaaaaaaaaaaaaaaaaa", revocation(), audit("session_revoke"))
	if err != nil || again {
		t.Fatalf("second RevokeSession = %v, %v; want false, nil", again, err)
	}
	err = s.RotateRefresh(ctx, store.Rotation{SessionID: "s1aaaaaaaaaaaaaaaaaaaaaaaa", ExpectVer: 1, RefreshHash: digest("x"), IdleExpiresAt: t0, LastUsedAt: t0}, audit("refresh"))
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("RotateRefresh on a revoked session = %v, want ErrUnavailable", err)
	}
}

func TestSetPasswordRevokesSessionsAndChecksVersion(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
	seedSessions(t, s, a, "s1aaaaaaaaaaaaaaaaaaaaaaaa")
	change := store.PasswordChange{TenantID: "shop", AccountID: a.ID, ExpectVer: 1, Hash: "new-hash", Revocation: revocation()}
	if err := s.SetPassword(ctx, change, audit("password_change")); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	got, _ := s.AccountByID(ctx, "shop", a.ID)
	if got.PasswordHash != "new-hash" || got.Ver != 2 || got.PasswordMustChange {
		t.Fatalf("account = %+v; want new hash, ver 2, no forced change", got)
	}
	if live, _ := s.ActiveSessions(ctx, "shop", a.ID); len(live) != 0 {
		t.Fatalf("active sessions after password change = %d, want 0", len(live))
	}
	if err := s.SetPassword(ctx, change, audit("password_change")); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("SetPassword with a stale version = %v, want ErrUnavailable", err)
	}
}

func TestConcurrentLoginFailuresAreAllCounted(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
	next := func(failed int) time.Time { return t0.Add(time.Duration(failed) * time.Second) }
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := s.RecordLoginFailure(ctx, "shop", a.ID, next, audit("login_failed")); err != nil {
				t.Errorf("RecordLoginFailure: %v", err)
			}
		})
	}
	wg.Wait()
	got, _ := s.AccountByID(ctx, "shop", a.ID)
	if got.FailedLoginCount != 4 || !got.NextLoginAt.Equal(t0.Add(4*time.Second)) {
		t.Fatalf("failed = %d, next = %v; want 4 and t0+4s", got.FailedLoginCount, got.NextLoginAt)
	}
	if err := s.CreateSession(ctx, newSession("s1aaaaaaaaaaaaaaaaaaaaaaaa", got), audit("login_ok")); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	after, _ := s.AccountByID(ctx, "shop", a.ID)
	if after.FailedLoginCount != 0 || !after.NextLoginAt.IsZero() {
		t.Fatalf("after login: failed = %d, next = %v; want reset", after.FailedLoginCount, after.NextLoginAt)
	}
}

func TestAuditRowsAreWrittenWithEachChange(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
	seedSessions(t, s, a, "s1aaaaaaaaaaaaaaaaaaaaaaaa")
	_, _ = s.RevokeSession(ctx, "s1aaaaaaaaaaaaaaaaaaaaaaaa", revocation(), audit("session_revoke"))
	rows, err := s.raw.Query(ctx, "SELECT action, count(*) FROM audit_events WHERE tenant_id = 'shop' GROUP BY action")
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	counts := map[string]int{}
	for rows.Next() {
		var action string
		var n int
		if err := rows.Scan(&action, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		counts[action] = n
	}
	for _, action := range []string{"tenant_create", "account_create", "login_ok", "session_revoke"} {
		if counts[action] != 1 {
			t.Fatalf("audit %s = %d, want 1 (all: %v)", action, counts[action], counts)
		}
	}
}
