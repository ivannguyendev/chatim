package app_test

import (
	"context"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/app"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
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
		t.Fatalf("iam exited: %v", err)
	default:
	}
}

func TestNotReadyUntilTheDirectoryIsLoaded(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{loadGate: make(chan struct{})}}
	r := runIAM(t, testConfig(kek), st, 3)
	r.logs.await(t, "iam not ready")
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
	if _, body := httpGet(t, r.admin, "/metrics"); !strings.Contains(body, "chatim_iam_directory_ready 0") {
		t.Fatal("metrics lack directory_ready 0 while loading")
	}
	close(st.loadGate)
	r.logs.await(t, "iam ready")
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
	_, body := httpGet(t, r.admin, "/metrics")
	for _, series := range []string{
		"chatim_iam_directory_ready 1", "chatim_iam_directory_entries 1", "chatim_iam_directory_load_seconds ",
		"chatim_iam_directory_load_failures_total 0", "chatim_iam_directory_lag_seconds ", "chatim_iam_directory_listen_up ",
		"chatim_iam_directory_horizon_stalled_seconds 0", "chatim_iam_directory_read_errors_total 0", "chatim_iam_directory_revocation_errors_total 0",
		`chatim_iam_directory_feed_requests_total{result="ok"} 0`, `chatim_iam_directory_feed_requests_total{result="empty"} 0`,
		`chatim_iam_directory_feed_requests_total{result="bad_request"} 0`, `chatim_iam_directory_feed_requests_total{result="unauthorized"} 0`,
		`chatim_iam_directory_feed_requests_total{result="error"} 0`, "chatim_iam_directory_feed_waiting 0",
		`chatim_iam_get_users_total{result="ok"} 0`, `chatim_iam_get_users_total{result="limited"} 0`, "chatim_iam_get_users_ids_total 0",
		"chatim_iam_user_limited_total 0", "chatim_iam_inflight_rejected_total 0", "chatim_iam_memory_limit_set 1",
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
	r := runIAM(t, cfg, st, 3)
	r.logs.await(t, "iam not ready")
	loaded := time.Now()
	close(st.loadGate)
	r.logs.await(t, "iam ready")
	if elapsed := time.Since(loaded); elapsed > 2*time.Second {
		t.Fatalf("ready %v after the load; want it at once, not on the 1h reload tick", elapsed)
	}
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
}

func TestBootsAndStaysAliveWhilePostgresIsDown(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{slotAsked: make(chan struct{}, 1)}}
	st.down.Store(true)
	r := runIAM(t, testConfig(kek), st, 3)
	r.logs.await(t, "iam not ready")
	receive(t, st.slotAsked, "first load attempt")
	receive(t, st.slotAsked, "load retry")
	requireRunning(t, r)
	requireStatus(t, r.admin, "/livez", http.StatusOK)
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
	st.down.Store(false)
	r.logs.await(t, "iam ready")
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
	if code, body := httpGet(t, r.api, "/.well-known/jwks.json"); code != http.StatusOK || !strings.Contains(body, st.keys[0].ID) {
		t.Fatalf("jwks after recovery = %d %s", code, body)
	}
}

func TestStaysReadyWhenPostgresDiesAfterTheFirstLoad(t *testing.T) {
	kek := newKEK(t)
	cfg := testConfig(kek)
	cfg.KeyReload = 20 * time.Millisecond
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{pings: make(chan struct{}, 1)}}
	r := runIAM(t, cfg, st, 3)
	r.logs.await(t, "iam ready")
	st.down.Store(true)
	for range 3 {
		receive(t, st.pings, "health check while postgres is down")
	}
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
	if code, body := httpGet(t, r.api, "/.well-known/jwks.json"); code != http.StatusOK || !strings.Contains(body, st.keys[0].ID) {
		t.Fatalf("jwks from memory = %d %s", code, body)
	}
}

var loadFailures = regexp.MustCompile(`chatim_iam_directory_load_failures_total [1-9]`)

func TestLivezStaysOkWhileLoadFails(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{slotAsked: make(chan struct{}, 1)}}
	st.down.Store(true)
	r := runIAM(t, testConfig(kek), st, 3)
	receive(t, st.slotAsked, "first load attempt")
	receive(t, st.slotAsked, "load retry")
	requireRunning(t, r)
	requireStatus(t, r.admin, "/livez", http.StatusOK)
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
	if _, body := httpGet(t, r.admin, "/metrics"); !loadFailures.MatchString(body) || !strings.Contains(body, "chatim_iam_directory_ready 0") {
		t.Fatal("metrics lack the failed loads")
	}
}

type pollResult struct {
	code int
	body string
	err  error
	at   time.Time
}

func longPoll(ctx context.Context, addr string) <-chan pollResult {
	out := make(chan pollResult, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/internal/v1/directory/changes?wait=10", http.NoBody)
		req.Header.Set("Authorization", "Bearer "+testNodeSecret)
		resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		if err != nil {
			out <- pollResult{err: err, at: time.Now()}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		out <- pollResult{code: resp.StatusCode, body: string(body), err: err, at: time.Now()}
	}()
	return out
}

func TestShutdownReleasesFeedWaitersBeforeClosingTheStore(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, outage: outage{feedAsked: make(chan int, 16)}}
	r := runIAM(t, testConfig(kek), st, 3)
	r.logs.await(t, "iam ready")
	poll := longPoll(t.Context(), r.internal)
	for limit := 0; limit != 500; {
		limit = receive(t, st.feedAsked, "the long poll reading the feed")
	}
	stopped := time.Now()
	r.cancel()
	got := receive(t, poll, "long poll answer")
	if got.err != nil || got.code != http.StatusOK || !strings.Contains(got.body, `"changes":[]`) {
		t.Fatalf("poll = %d %s (%v); want an empty 200", got.code, got.body, got.err)
	}
	if waited := got.at.Sub(stopped); waited > 5*time.Second {
		t.Fatalf("poll answered %v after shutdown began; want it released before its 10s wait", waited)
	}
	if err := receive(t, r.done, "run return"); err != nil {
		t.Fatalf("Run = %v; want nil", err)
	}
	if closed, last := st.closedAt.Load(), st.lastFeed.Load(); closed <= last {
		t.Fatalf("store closed at step %d before the last feed read at step %d", closed, last)
	}
}

func TestMemoryLimitUnsetIsReported(t *testing.T) {
	kek := newKEK(t)
	r := runIAM(t, testConfig(kek), &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3}, 3,
		func(d *app.Deps) { d.MemoryLimit = func() int64 { return math.MaxInt64 } })
	r.logs.await(t, "GOMEMLIMIT is not set; set it to about 90% of the container memory limit")
	if _, body := httpGet(t, r.admin, "/metrics"); !strings.Contains(body, "chatim_iam_memory_limit_set 0") {
		t.Fatal("metrics lack memory_limit_set 0")
	}
}
