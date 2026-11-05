package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const promptStop = 2 * time.Second

func TestBootServesLivezWhileMongoHangs(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, outage: outage{pings: make(chan struct{}, 1)}}
	st.hangPing.Store(true)
	r := runIdentity(t, testConfig(kek), st)
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
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, outage: outage{
		loadGate: make(chan struct{}), slotAsked: make(chan struct{}, 1), streamOpen: make(chan struct{}, 1),
	}}
	r := runIdentity(t, testConfig(kek), st)
	receive(t, st.slotAsked, "directory load")
	st.hangPing.Store(true)
	close(st.loadGate)
	receive(t, st.streamOpen, "follower start")
	if done := st.pingsDone.Load(); done != 0 {
		t.Fatalf("follower started only after %d hanging pings ended", done)
	}
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
}

func TestNoPingAfterFirstReady(t *testing.T) {
	kek := newKEK(t)
	cfg := testConfig(kek)
	cfg.KeyReload = 20 * time.Millisecond
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, outage: outage{reloads: make(chan struct{}, 1)}}
	r := runIdentity(t, cfg, st)
	r.logs.await(t, "identity ready")
	pings := st.pingCount.Load()
	for range 3 {
		receive(t, st.reloads, "key reload after the first ready")
	}
	if got := st.pingCount.Load(); got != pings {
		t.Fatalf("pings = %d after the first ready; want %d", got, pings)
	}
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
}
