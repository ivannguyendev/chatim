package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
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
		if err := s.CreateSigningKey(ctx, pendingKey(id), audit("key_create")); err != nil {
			t.Fatalf("CreateSigningKey %s: %v", id, err)
		}
	}
	if err := s.ActivateSigningKey(ctx, "k-first", t0, audit("key_activate")); err != nil {
		t.Fatalf("activate first: %v", err)
	}
	if err := s.ActivateSigningKey(ctx, "k-second", t0.Add(time.Hour), audit("key_activate")); err != nil {
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
	if err := s.ActivateSigningKey(ctx, "k-missing", t0, audit("key_activate")); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("activate unknown key = %v, want ErrNotFound", err)
	}
	_, err = s.raw.Exec(ctx, "INSERT INTO signing_keys (kid, state, public_key, private_key_sealed, created_at) VALUES ('k-third', 2, '\\x00', '\\x00', now())")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("second active key insert = %v, want unique violation", err)
	}
	if err := s.CreateSigningKey(ctx, pendingKey("k-first"), audit("key_create")); !errors.Is(err, apperr.ErrAlreadyExists) {
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

func createKey(t *testing.T, s fixture, id string, created time.Time) {
	t.Helper()
	k := pendingKey(id)
	k.CreatedAt = created
	if err := s.CreateSigningKey(context.Background(), k, audit("key_create")); err != nil {
		t.Fatalf("CreateSigningKey %s: %v", id, err)
	}
}

func TestDuePendingKeyIsActivatedOnce(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	createKey(t, s, "k-old", t0)
	if err := s.ActivateSigningKey(ctx, "k-old", t0, audit("key_activate")); err != nil {
		t.Fatalf("activate old: %v", err)
	}
	createKey(t, s, "k-next", t0.Add(time.Minute))
	if kid, err := s.ActivateDueSigningKey(ctx, t0.Add(30*time.Second), t0.Add(30*time.Second), audit("key_activate")); err != nil || kid != "" {
		t.Fatalf("before the lead = %q, %v; want nothing", kid, err)
	}
	results := make(chan string, 2)
	for range 2 {
		go func() {
			kid, err := s.ActivateDueSigningKey(ctx, t0.Add(3*time.Minute), t0.Add(3*time.Minute), audit("key_activate"))
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
	createKey(t, s, "k-a", t0)
	createKey(t, s, "k-b", t0.Add(time.Minute))
	createKey(t, s, "k-c", t0.Add(2*time.Minute))
	if err := s.ActivateSigningKey(ctx, "k-b", t0.Add(time.Hour), audit("key_activate")); err != nil {
		t.Fatalf("activate b: %v", err)
	}
	if st := keyStates(t, s); st["k-a"] != store.KeyRetired || st["k-b"] != store.KeyActive || st["k-c"] != store.KeyPending {
		t.Fatalf("states = %v; want a retired, b active, c pending", st)
	}
	if err := s.ActivateSigningKey(ctx, "k-a", t0.Add(2*time.Hour), audit("key_activate")); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("activate retired = %v, want ErrFailedPrecondition", err)
	}
	if kid, err := s.ActivateDueSigningKey(ctx, t0.Add(3*time.Hour), t0.Add(3*time.Hour), audit("key_activate")); err != nil || kid != "k-c" {
		t.Fatalf("due = %q, %v; want k-c", kid, err)
	}
	if st := keyStates(t, s); st["k-a"] != store.KeyRetired || st["k-b"] != store.KeyRetired || st["k-c"] != store.KeyActive {
		t.Fatalf("states = %v", st)
	}
}

func TestSessionRoundTripKeepsHistoryAndJTIs(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
	seedSessions(t, s, a, "s1aaaaaaaaaaaaaaaaaaaaaaaa")
	history := []session.HistoryEntry{{Hash: digest("h1"), ReplacedAt: t0.Add(time.Minute)}, {Hash: digest("h0"), ReplacedAt: t0}}
	rot := store.Rotation{SessionID: "s1aaaaaaaaaaaaaaaaaaaaaaaa", ExpectVer: 1, RefreshHash: digest("h2"), History: history, RecentJTI: []string{"j2", "j1"}, IdleExpiresAt: t0.Add(2 * time.Hour), LastUsedAt: t0.Add(time.Minute)}
	if _, err := s.RotateRefresh(ctx, rot, audit("refresh")); err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}
	got, err := s.SessionByID(ctx, "s1aaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if string(got.RefreshHash) != string(digest("h2")) || len(got.History) != 2 || !got.History[0].ReplacedAt.Equal(t0.Add(time.Minute)) || string(got.History[1].Hash) != string(digest("h0")) {
		t.Fatalf("history did not round trip: %+v", got.History)
	}
	if len(got.RecentJTI) != 2 || got.RecentJTI[0] != "j2" || got.KeyThumbprint != "jkt-s1aaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("jti/thumbprint = %v, %s", got.RecentJTI, got.KeyThumbprint)
	}
	if !got.IdleExpiresAt.Equal(t0.Add(2*time.Hour)) || !got.LastUsedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("times = %v, %v", got.IdleExpiresAt, got.LastUsedAt)
	}
}

func BenchmarkRotateRefresh(b *testing.B) {
	s := openStore(b)
	ctx := context.Background()
	seedTenant(b, s, "shop")
	a := seedAccount(b, s, "shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan")
	seedSessions(b, s, a, "s1aaaaaaaaaaaaaaaaaaaaaaaa")
	history := []session.HistoryEntry{{Hash: digest("h0"), ReplacedAt: t0}}
	ver := int64(1)
	b.ResetTimer()
	for b.Loop() {
		_, err := s.RotateRefresh(ctx, store.Rotation{
			SessionID: "s1aaaaaaaaaaaaaaaaaaaaaaaa", ExpectVer: ver, RefreshHash: digest("h"),
			History: history, RecentJTI: []string{"j"}, IdleExpiresAt: t0.Add(time.Hour), LastUsedAt: t0,
		}, audit("refresh"))
		if err != nil {
			b.Fatal(err)
		}
		ver++
	}
}
