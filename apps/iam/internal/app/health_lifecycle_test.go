package app_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/app"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const promptStop = 2 * time.Second

func TestBootServesLivezWhilePostgresHangs(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{pings: make(chan struct{}, 1)}}
	st.hangPing.Store(true)
	r := runIAM(t, testConfig(kek), st, 3)
	receive(t, st.pings, "boot health check")
	requireStatus(t, r.admin, "/livez", http.StatusOK)
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
	if done := st.pingsDone.Load(); done != 0 {
		t.Fatalf("livez answered only after %d hanging pings ended", done)
	}
	stopped := time.Now()
	r.cancel()
	if err := receive(t, r.done, "run return"); err != nil {
		t.Fatalf("Run = %v; want nil", err)
	}
	if took := time.Since(stopped); took > promptStop {
		t.Fatalf("shutdown took %v with a hanging ping; want under %v", took, promptStop)
	}
}

func TestLoadedDirectoryStartsTheFollowerEvenIfAHealthCheckHangs(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3}
	st.hangPing.Store(true)
	followed := make(chan struct{}, 1)
	r := runIAM(t, testConfig(kek), st, 3, func(d *app.Deps) {
		d.OpenListener = func(context.Context, string) (store.Listener, error) {
			signal(followed)
			return idleListener{}, nil
		}
	})
	receive(t, followed, "follower start")
	if done := st.pingsDone.Load(); done != 0 {
		t.Fatalf("follower started only after %d hanging pings ended", done)
	}
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
}

func TestNoPingAfterFirstReady(t *testing.T) {
	kek := newKEK(t)
	cfg := testConfig(kek)
	cfg.KeyReload = 20 * time.Millisecond
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{reloads: make(chan struct{}, 1)}}
	r := runIAM(t, cfg, st, 3)
	r.logs.await(t, "iam ready")
	pings := st.pingCount.Load()
	for range 3 {
		receive(t, st.reloads, "key reload after the first ready")
	}
	if got := st.pingCount.Load(); got != pings {
		t.Fatalf("pings = %d after the first ready; want %d", got, pings)
	}
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
}
