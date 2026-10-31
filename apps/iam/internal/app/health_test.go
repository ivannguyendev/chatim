package app

import (
	"context"
	"crypto/rand"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/keyring"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/admin"
)

type hangingStore struct {
	Store
	keys     []store.SigningKey
	hangPing atomic.Bool
	hangKeys atomic.Bool
	pings    atomic.Int64
}

func (s *hangingStore) SigningKeys(ctx context.Context) ([]store.SigningKey, error) {
	if s.hangKeys.Load() {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.keys, nil
}

func (s *hangingStore) Ping(ctx context.Context) error {
	s.pings.Add(1)
	if s.hangPing.Load() {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (s *hangingStore) SchemaVersion(context.Context) (int, error) { return 3, nil }

func (s *hangingStore) ActivateDueSigningKey(ctx context.Context, _ time.Duration, _ store.AuditEvent) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (s *hangingStore) DeleteExpiredRevocations(ctx context.Context) (int64, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

type loadedDirectory struct{}

func (loadedDirectory) Ready() bool { return true }

func newHealthUnderTest(t *testing.T) (*health, *hangingStore) {
	t.Helper()
	kek := make([]byte, signing.KEKLength)
	_, _ = rand.Read(kek)
	sealer, err := signing.NewSealer(kek, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := signing.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal(k)
	if err != nil {
		t.Fatal(err)
	}
	st := &hangingStore{keys: []store.SigningKey{{ID: k.ID, State: store.KeyActive, PublicKey: k.PublicKey().Key, PrivateSealed: sealed}}}
	log := slog.New(slog.DiscardHandler)
	ring := keyring.New(st, sealer, keyring.Config{Token: signing.TokenConfig{Issuer: "iss", Audience: "aud", TTL: time.Minute}})
	return newHealth(admin.New(admin.Config{Addr: "admin"}, log), log, st, ring, loadedDirectory{}, 3), st
}

func (h *health) state() (checked, ready bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.checked, h.ready
}

func timed(f func()) time.Duration {
	began := time.Now()
	f()
	return time.Since(began)
}

func TestHealthCheckTimesOutAndStaysNotReadyBeforeTheFirstReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, st := newHealthUnderTest(t)
		st.hangPing.Store(true)
		if took := timed(func() { h.check(t.Context()) }); took != storeCallTimeout {
			t.Fatalf("check took %v; want the %v timeout", took, storeCallTimeout)
		}
		if checked, ready := h.state(); !checked || ready {
			t.Fatalf("checked=%v ready=%v; want a failed check", checked, ready)
		}
	})
}

func TestNoPingAfterFirstReadyAndAHangingReloadKeepsReadiness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, st := newHealthUnderTest(t)
		h.check(t.Context())
		if _, ready := h.state(); !ready {
			t.Fatal("not ready with a healthy store")
		}
		pings := st.pings.Load()
		st.hangPing.Store(true)
		st.hangKeys.Store(true)
		if took := timed(func() { h.check(t.Context()) }); took != storeCallTimeout {
			t.Fatalf("check took %v; want the %v timeout", took, storeCallTimeout)
		}
		if _, ready := h.state(); !ready {
			t.Fatal("lost readiness on a timed-out check after the first ready")
		}
		if got := st.pings.Load(); got != pings {
			t.Fatalf("pings = %d after the first ready; want %d", got, pings)
		}
	})
}

func TestShutdownCancelsARunningHealthCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, st := newHealthUnderTest(t)
		st.hangPing.Store(true)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan time.Duration, 1)
		go func() { done <- timed(func() { h.check(ctx) }) }()
		time.Sleep(time.Second)
		h.stop()
		cancel()
		if took := <-done; took != time.Second {
			t.Fatalf("check returned %v after it began; want it at the 1s cancel", took)
		}
		if _, ready := h.state(); ready {
			t.Fatal("ready after stop")
		}
	})
}

func TestKeyActivationAndCleanupAreBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := &node{log: slog.New(slog.DiscardHandler), store: &hangingStore{}}
		if took := timed(func() { n.activateDueKey(t.Context()) }); took != storeCallTimeout {
			t.Fatalf("key activation took %v; want the %v timeout", took, storeCallTimeout)
		}
		if took := timed(func() { n.cleanExpired(t.Context()) }); took != storeCallTimeout {
			t.Fatalf("cleanup took %v; want the %v timeout", took, storeCallTimeout)
		}
		if n.cleanup.errors.Load() != 1 {
			t.Fatal("timed-out cleanup not counted as an error")
		}
	})
}
