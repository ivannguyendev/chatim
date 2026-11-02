package pgstore_test

import (
	"context"
	"errors"
	"sync"
	"testing"

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
			s, err := pgstore.Open(ctx, uri, pgstore.Options{MaxConns: 2, RevocationKeep: testKeep})
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

func TestSetUserStateBumpsVerAndStaysInsideTheTenant(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	seedTenant(t, s, "blog")
	seedUser(t, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
	if err := s.SetUserState(ctx, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", store.StateDisabled, audit("user_disable")); err != nil {
		t.Fatalf("SetUserState: %v", err)
	}
	var state store.State
	var ver int64
	if err := s.raw.QueryRow(ctx, "SELECT state, ver FROM users WHERE tenant_id = 'shop' AND id = 'a1aaaaaaaaaaaaaaaaaaaaaaaa'").Scan(&state, &ver); err != nil {
		t.Fatal(err)
	}
	if state != store.StateDisabled || ver != 2 {
		t.Fatalf("state, ver = %d, %d; want disabled, 2", state, ver)
	}
	if err := s.SetUserState(ctx, "blog", "a1aaaaaaaaaaaaaaaaaaaaaaaa", store.StateDisabled, audit("user_disable")); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("user changed through another tenant = %v, want ErrNotFound", err)
	}
}
