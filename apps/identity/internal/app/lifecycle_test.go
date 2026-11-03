package app_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/app"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/mongostore"
)

func TestReadyWithAnActiveKeyAndTheCurrentSchema(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}}
	r := runIdentity(t, testConfig(kek), st)
	r.logs.await(t, "identity ready")
	if code, _ := httpGet(t, r.admin, "/readyz"); code != http.StatusOK {
		t.Fatalf("readyz = %d, want 200", code)
	}
	code, body := httpGet(t, r.admin, "/metrics")
	if code != http.StatusOK || strings.Contains(body, "chatim_identity_login_total") || strings.Contains(body, "chatim_identity_hash_inflight") || !strings.Contains(body, "chatim_identity_signing_key_ready 1") {
		t.Fatalf("metrics = %d, want the key series and no login series", code)
	}
	if code, _ := httpGet(t, r.api, "/nope"); code != http.StatusNotFound {
		t.Fatalf("unknown api path = %d, want 404", code)
	}
	r.cancel()
	if err := receive(t, r.done, "run return"); err != nil {
		t.Fatalf("Run = %v, want nil after cancel", err)
	}
	if !st.closed.Load() {
		t.Fatal("store not closed on shutdown")
	}
}

func TestNotReadyWithoutAnActiveKey(t *testing.T) {
	kek := newKEK(t)
	r := runIdentity(t, testConfig(kek), &fakeStore{})
	r.logs.await(t, "identity not ready")
	if code, body := httpGet(t, r.admin, "/metrics"); code != http.StatusOK || !strings.Contains(body, "chatim_identity_signing_key_ready 0") {
		t.Fatalf("metrics = %d, want signing_key_ready 0", code)
	}
}

func TestRunRefusesBadConfigAndStoreFailures(t *testing.T) {
	kek := newKEK(t)
	bad := testConfig(kek)
	bad.Issuer = ""
	if err := app.Run(context.Background(), bad, slog.New(slog.DiscardHandler), app.Deps{}); err == nil {
		t.Fatal("Run accepted a config without an issuer")
	}
	deps := app.Deps{OpenStore: func(context.Context, mongostore.Options) (app.Store, error) { return nil, errOpen }}
	if err := app.Run(context.Background(), testConfig(kek), slog.New(slog.DiscardHandler), deps); !errors.Is(err, errOpen) {
		t.Fatalf("Run = %v, want the store error", err)
	}
}

func TestMainRefusesBadArguments(t *testing.T) {
	for _, args := range [][]string{{"nope"}, {"serve", "extra"}} {
		if code := app.Main(args); code != 2 {
			t.Fatalf("Main(%v) = %d, want 2", args, code)
		}
	}
}
