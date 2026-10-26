package pgstore_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMigrateTwiceAppliesNothingTheSecondTime(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	pending, err := s.Migrate(ctx, false)
	if err != nil || len(pending) != 0 {
		t.Fatalf("second Migrate = %v, %v; want none pending", pending, err)
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != pgstore.LatestVersion() {
		t.Fatalf("SchemaVersion = %d, %v; want %d", v, err, pgstore.LatestVersion())
	}
}

func TestConcurrentMigrationsDoNotOverlap(t *testing.T) {
	ctx := context.Background()
	uri := testDatabaseURI(t)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Go(func() {
			s, err := pgstore.Open(ctx, uri, 2)
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			_, err = s.Migrate(ctx, true)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Migrate: %v", err)
		}
	}
}

func TestUsernameIsUniquePerTenant(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedTenant(t, s, "blog")
	seedAccount(t, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
	dup := store.Account{TenantID: "shop", ID: "a2aaaaaaaaaaaaaaaaaaaaaaaa", Username: "lan", State: store.StateActive, PasswordHash: "x", Ver: 1}
	if err := s.CreateAccount(context.Background(), dup, audit("account_create")); !errors.Is(err, apperr.ErrAlreadyExists) {
		t.Fatalf("duplicate username = %v, want ErrAlreadyExists", err)
	}
	seedAccount(t, s, "blog", "a3aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
	if _, err := s.AccountByUsername(context.Background(), "blog", "lan"); err != nil {
		t.Fatalf("same username in another tenant: %v", err)
	}
	if _, err := s.AccountByID(context.Background(), "blog", "a1aaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("account read through another tenant = %v, want ErrNotFound", err)
	}
}

func TestConcurrentRotationsLetExactlyOneWin(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
	if err := s.CreateSession(ctx, newSession("s1aaaaaaaaaaaaaaaaaaaaaaaa", a), audit("login_ok")); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range 2 {
		wg.Go(func() {
			results <- s.RotateRefresh(ctx, store.Rotation{
				SessionID: "s1aaaaaaaaaaaaaaaaaaaaaaaa", ExpectVer: 1,
				RefreshHash: digest("token-2-" + string(rune('a'+i))),
				History:     []session.HistoryEntry{{Hash: digest("s1-token-1"), ReplacedAt: t0}},
				RecentJTI:   []string{"jti"}, IdleExpiresAt: t0.Add(time.Hour), LastUsedAt: t0,
			}, audit("refresh"))
		})
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, apperr.ErrUnavailable):
			conflicts++
		default:
			t.Fatalf("RotateRefresh: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins = %d, conflicts = %d; want 1 and 1", wins, conflicts)
	}
	got, _ := s.SessionByID(ctx, "s1aaaaaaaaaaaaaaaaaaaaaaaa")
	if got.RefreshVer != 2 || len(got.History) != 1 || len(got.RecentJTI) != 1 {
		t.Fatalf("session after rotation = ver %d, history %d, jti %d; want 2, 1, 1", got.RefreshVer, len(got.History), len(got.RecentJTI))
	}
}

func TestCreateSessionFailsWhenTheAccountChangedMidLogin(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(s fixture){
		"account disabled": func(s fixture) {
			_ = s.SetAccountState(ctx, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", store.StateDisabled, revocation(), audit("account_disable"))
		},
		"password changed": func(s fixture) {
			_ = s.SetPassword(ctx, store.PasswordChange{TenantID: "shop", AccountID: "a1aaaaaaaaaaaaaaaaaaaaaaaa", ExpectVer: 1, Hash: "new", Revocation: revocation()}, audit("password_change"))
		},
		"tenant disabled": func(s fixture) {
			_ = s.SetTenantState(ctx, "shop", store.StateDisabled, revocation(), audit("tenant_disable"))
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := openStore(t)
			seedTenant(t, s, "shop")
			a := seedAccount(t, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
			change(s)
			err := s.CreateSession(ctx, newSession("s1aaaaaaaaaaaaaaaaaaaaaaaa", a), audit("login_ok"))
			if !errors.Is(err, apperr.ErrUnavailable) {
				t.Fatalf("CreateSession after %s = %v, want ErrUnavailable", name, err)
			}
			if _, err := s.SessionByID(ctx, "s1aaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, apperr.ErrNotFound) {
				t.Fatalf("session exists after a rejected login: %v", err)
			}
		})
	}
}
