package app_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/app"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

func TestReadyWithAnActiveKeyAndTheCurrentSchema(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3}
	r := runIAM(t, testConfig(kek), st, 3)
	r.logs.await(t, "iam ready")
	if code, _ := httpGet(t, r.admin, "/readyz"); code != http.StatusOK {
		t.Fatalf("readyz = %d, want 200", code)
	}
	code, body := httpGet(t, r.admin, "/metrics")
	if code != http.StatusOK || !strings.Contains(body, `chatim_iam_login_total{result="ok"}`) || !strings.Contains(body, "chatim_iam_signing_key_ready 1") {
		t.Fatalf("metrics = %d, missing iam series", code)
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

func TestNotReadyWhenTheSchemaIsBehind(t *testing.T) {
	kek := newKEK(t)
	r := runIAM(t, testConfig(kek), &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 2}, 3)
	r.logs.await(t, "iam not ready")
	if code, _ := httpGet(t, r.admin, "/readyz"); code == http.StatusOK {
		t.Fatal("ready with an old schema")
	}
}

func TestNotReadyWithoutAnActiveKey(t *testing.T) {
	kek := newKEK(t)
	r := runIAM(t, testConfig(kek), &fakeStore{version: 3}, 3)
	r.logs.await(t, "iam not ready")
	if code, body := httpGet(t, r.admin, "/metrics"); code != http.StatusOK || !strings.Contains(body, "chatim_iam_signing_key_ready 0") {
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
	deps := app.Deps{OpenStore: func(context.Context, string, pgstore.Options) (app.Store, error) { return nil, errOpen }}
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
