package app_test

import (
	"math"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/app"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

func requireStatus(t *testing.T, addr, path string, want int) {
	t.Helper()
	if code, body := httpGet(t, addr, path); code != want {
		t.Fatalf("%s = %d %s; want %d", path, code, body, want)
	}
}

func requireRunning(t *testing.T, r *running) {
	t.Helper()
	select {
	case err := <-r.done:
		t.Fatalf("identity exited: %v", err)
	default:
	}
}

func TestNotReadyUntilTheDirectoryIsLoaded(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{loadGate: make(chan struct{})}}
	r := runIdentity(t, testConfig(kek), st, 3)
	r.logs.await(t, "identity not ready")
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
	if _, body := httpGet(t, r.admin, "/metrics"); !strings.Contains(body, "chatim_identity_directory_ready 0") {
		t.Fatal("metrics lack directory_ready 0 while loading")
	}
	close(st.loadGate)
	r.logs.await(t, "identity ready")
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
	_, body := httpGet(t, r.admin, "/metrics")
	for _, series := range []string{
		"chatim_identity_directory_ready 1", "chatim_identity_directory_entries 1", "chatim_identity_directory_load_seconds ",
		"chatim_identity_directory_load_failures_total 0", "chatim_identity_directory_lag_seconds ", "chatim_identity_directory_listen_up ",
		"chatim_identity_directory_horizon_stalled_seconds 0", "chatim_identity_directory_read_errors_total 0", "chatim_identity_directory_revocation_errors_total 0",
		`chatim_identity_get_users_total{result="ok"} 0`, `chatim_identity_get_users_total{result="limited"} 0`, "chatim_identity_get_users_ids_total 0",
		"chatim_identity_user_limited_total 0", "chatim_identity_inflight_rejected_total 0", "chatim_identity_memory_limit_set 1",
	} {
		if !strings.Contains(body, series) {
			t.Errorf("metrics lack %q", series)
		}
	}
}

func TestReadyRightAfterTheLoadWithoutWaitingForTheReloadTick(t *testing.T) {
	kek := newKEK(t)
	cfg := testConfig(kek)
	cfg.KeyReload = time.Hour
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{loadGate: make(chan struct{})}}
	r := runIdentity(t, cfg, st, 3)
	r.logs.await(t, "identity not ready")
	loaded := time.Now()
	close(st.loadGate)
	r.logs.await(t, "identity ready")
	if elapsed := time.Since(loaded); elapsed > 2*time.Second {
		t.Fatalf("ready %v after the load; want it at once, not on the 1h reload tick", elapsed)
	}
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
}

func TestBootsAndStaysAliveWhilePostgresIsDown(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{slotAsked: make(chan struct{}, 1)}}
	st.down.Store(true)
	r := runIdentity(t, testConfig(kek), st, 3)
	r.logs.await(t, "identity not ready")
	receive(t, st.slotAsked, "first load attempt")
	receive(t, st.slotAsked, "load retry")
	requireRunning(t, r)
	requireStatus(t, r.admin, "/livez", http.StatusOK)
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
	st.down.Store(false)
	r.logs.await(t, "identity ready")
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
	if code, body := httpGet(t, r.api, "/.well-known/jwks.json"); code != http.StatusOK || !strings.Contains(body, st.keys[0].ID) {
		t.Fatalf("jwks after recovery = %d %s", code, body)
	}
}

func TestStaysReadyWhenPostgresDiesAfterTheFirstLoad(t *testing.T) {
	kek := newKEK(t)
	cfg := testConfig(kek)
	cfg.KeyReload = 20 * time.Millisecond
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{reloads: make(chan struct{}, 1)}}
	r := runIdentity(t, cfg, st, 3)
	r.logs.await(t, "identity ready")
	st.down.Store(true)
	for range 3 {
		receive(t, st.reloads, "health check while postgres is down")
	}
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
	if code, body := httpGet(t, r.api, "/.well-known/jwks.json"); code != http.StatusOK || !strings.Contains(body, st.keys[0].ID) {
		t.Fatalf("jwks from memory = %d %s", code, body)
	}
}

var loadFailures = regexp.MustCompile(`chatim_identity_directory_load_failures_total [1-9]`)

func TestLivezStaysOkWhileLoadFails(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{slotAsked: make(chan struct{}, 1)}}
	st.down.Store(true)
	r := runIdentity(t, testConfig(kek), st, 3)
	receive(t, st.slotAsked, "first load attempt")
	receive(t, st.slotAsked, "load retry")
	requireRunning(t, r)
	requireStatus(t, r.admin, "/livez", http.StatusOK)
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
	if _, body := httpGet(t, r.admin, "/metrics"); !loadFailures.MatchString(body) || !strings.Contains(body, "chatim_identity_directory_ready 0") {
		t.Fatal("metrics lack the failed loads")
	}
}

func TestMemoryLimitUnsetIsReported(t *testing.T) {
	kek := newKEK(t)
	r := runIdentity(t, testConfig(kek), &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3}, 3,
		func(d *app.Deps) { d.MemoryLimit = func() int64 { return math.MaxInt64 } })
	r.logs.await(t, "GOMEMLIMIT is not set; set it to about 90% of the container memory limit")
	if _, body := httpGet(t, r.admin, "/metrics"); !strings.Contains(body, "chatim_identity_memory_limit_set 0") {
		t.Fatal("metrics lack memory_limit_set 0")
	}
}
