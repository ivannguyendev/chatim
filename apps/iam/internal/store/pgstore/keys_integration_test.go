package pgstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func pendingKey(id string) store.SigningKey {
	return store.SigningKey{ID: id, State: store.KeyPending, PublicKey: []byte("pub-" + id), PrivateSealed: []byte("sealed-" + id), CreatedAt: t0}
}

func TestExactlyOneSigningKeyIsActive(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	for _, id := range []string{"k-first", "k-second"} {
		if _, err := s.CreateSigningKey(ctx, pendingKey(id), audit("key_create")); err != nil {
			t.Fatalf("CreateSigningKey %s: %v", id, err)
		}
	}
	if err := s.ActivateSigningKey(ctx, "k-first", 0, audit("key_activate")); err != nil {
		t.Fatalf("activate first: %v", err)
	}
	if err := s.ActivateSigningKey(ctx, "k-second", 0, audit("key_activate")); err != nil {
		t.Fatalf("activate second: %v", err)
	}
	keys, err := s.SigningKeys(ctx)
	if err != nil {
		t.Fatalf("SigningKeys: %v", err)
	}
	states := map[string]store.KeyState{}
	for _, k := range keys {
		states[k.ID] = k.State
	}
	if states["k-first"] != store.KeyRetired || states["k-second"] != store.KeyActive {
		t.Fatalf("states = %v; want first retired, second active", states)
	}
	if err := s.ActivateSigningKey(ctx, "k-missing", 0, audit("key_activate")); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("activate unknown key = %v, want ErrNotFound", err)
	}
	_, err = s.raw.Exec(ctx, "INSERT INTO signing_keys (kid, state, public_key, private_key_sealed, created_at) VALUES ('k-third', 2, '\\x00', '\\x00', now())")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("second active key insert = %v, want unique violation", err)
	}
	if _, err := s.CreateSigningKey(ctx, pendingKey("k-first"), audit("key_create")); !errors.Is(err, apperr.ErrAlreadyExists) {
		t.Fatalf("duplicate kid = %v, want ErrAlreadyExists", err)
	}
}

func keyStates(t *testing.T, s fixture) map[string]store.KeyState {
	t.Helper()
	keys, err := s.SigningKeys(context.Background())
	if err != nil {
		t.Fatalf("SigningKeys: %v", err)
	}
	out := map[string]store.KeyState{}
	for _, k := range keys {
		out[k.ID] = k.State
	}
	return out
}

func createKey(t *testing.T, s fixture, id string) time.Time {
	t.Helper()
	k := pendingKey(id)
	k.CreatedAt = t0
	created, err := s.CreateSigningKey(context.Background(), k, audit("key_create"))
	if err != nil {
		t.Fatalf("CreateSigningKey %s: %v", id, err)
	}
	return created
}

func TestKeyTimesComeFromPostgres(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	before := time.Now()
	if created := createKey(t, s, "k-now"); created.Before(before.Add(-time.Minute)) || created.Equal(t0) {
		t.Fatalf("created_at = %v; want the database clock, not the caller's %v", created, t0)
	}
	err := s.ActivateSigningKey(ctx, "k-now", time.Hour, audit("key_activate"))
	if !errors.Is(err, apperr.ErrFailedPrecondition) || !strings.Contains(err.Error(), "can be activated from") {
		t.Fatalf("activate inside the lead = %v, want ErrFailedPrecondition", err)
	}
	if st := keyStates(t, s); st["k-now"] != store.KeyPending {
		t.Fatalf("states = %v; want k-now still pending", st)
	}
}

func TestDuePendingKeyIsActivatedOnce(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	createKey(t, s, "k-old")
	if err := s.ActivateSigningKey(ctx, "k-old", 0, audit("key_activate")); err != nil {
		t.Fatalf("activate old: %v", err)
	}
	createKey(t, s, "k-next")
	if kid, err := s.ActivateDueSigningKey(ctx, time.Hour, audit("key_activate")); err != nil || kid != "" {
		t.Fatalf("before the lead = %q, %v; want nothing", kid, err)
	}
	results := make(chan string, 2)
	for range 2 {
		go func() {
			kid, err := s.ActivateDueSigningKey(ctx, 0, audit("key_activate"))
			if err != nil {
				t.Errorf("ActivateDueSigningKey: %v", err)
			}
			results <- kid
		}()
	}
	got := []string{<-results, <-results}
	if (got[0] == "k-next") == (got[1] == "k-next") || got[0]+got[1] != "k-next" {
		t.Fatalf("two replicas activated %q; want k-next exactly once", got)
	}
	if st := keyStates(t, s); st["k-old"] != store.KeyRetired || st["k-next"] != store.KeyActive {
		t.Fatalf("states = %v", st)
	}
}

func TestActivationRetiresOlderPendingKeysAndNeverRevivesARetiredOne(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	createKey(t, s, "k-a")
	createKey(t, s, "k-b")
	createKey(t, s, "k-c")
	if err := s.ActivateSigningKey(ctx, "k-b", 0, audit("key_activate")); err != nil {
		t.Fatalf("activate b: %v", err)
	}
	if st := keyStates(t, s); st["k-a"] != store.KeyRetired || st["k-b"] != store.KeyActive || st["k-c"] != store.KeyPending {
		t.Fatalf("states = %v; want a retired, b active, c pending", st)
	}
	if err := s.ActivateSigningKey(ctx, "k-a", 0, audit("key_activate")); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("activate retired = %v, want ErrFailedPrecondition", err)
	}
	if kid, err := s.ActivateDueSigningKey(ctx, 0, audit("key_activate")); err != nil || kid != "k-c" {
		t.Fatalf("due = %q, %v; want k-c", kid, err)
	}
	if st := keyStates(t, s); st["k-a"] != store.KeyRetired || st["k-b"] != store.KeyRetired || st["k-c"] != store.KeyActive {
		t.Fatalf("states = %v", st)
	}
}

func TestDueKeyNeverRollsBackToAPendingKeyOlderThanTheActiveOne(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	createKey(t, s, "k-stale")
	createKey(t, s, "k-live")
	if _, err := s.raw.Exec(ctx, "UPDATE signing_keys SET state = 2, activated_at = now() WHERE kid = 'k-live'"); err != nil {
		t.Fatal(err)
	}
	if kid, err := s.ActivateDueSigningKey(ctx, 0, audit("key_activate")); err != nil || kid != "" {
		t.Fatalf("due = %q, %v; want nothing older than the active key", kid, err)
	}
	if st := keyStates(t, s); st["k-live"] != store.KeyActive {
		t.Fatalf("states = %v; want k-live still active", st)
	}
}
