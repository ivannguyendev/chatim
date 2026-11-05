package app_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

func requireMetrics(t *testing.T, addr string, series ...string) {
	t.Helper()
	_, body := httpGet(t, addr, "/metrics")
	for _, s := range series {
		if !strings.Contains(body, s) {
			t.Errorf("metrics lack %q", s)
		}
	}
}

var fiveMinutes = regexp.MustCompile(`"expires_in":(299|300)}`)

func TestReadyOnlyAfterTheFirstTrustLoadAndExchangesServed(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, trustFake: trustFake{trustGate: make(chan struct{}), tenantKey: newTenantKey(t)}}
	r := runIdentity(t, testConfig(kek), st)
	r.logs.await(t, "identity not ready")
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
	if code, body := postExchange(t, r.api, tenantToken(t, st.tenantKey, "lan")); code != http.StatusServiceUnavailable {
		t.Fatalf("exchange before the trust load = %d %s; want 503", code, body)
	}
	close(st.trustGate)
	r.logs.await(t, "identity ready")
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
	code, body := postExchange(t, r.api, tenantToken(t, st.tenantKey, "lan"))
	if code != http.StatusOK || !fiveMinutes.MatchString(body) || !strings.Contains(body, `"token_type":"Bearer"`) {
		t.Fatalf("exchange = %d %s; want 200 with a 5 minute chat token", code, body)
	}
	if code, body := postExchange(t, r.api, tenantToken(t, newTenantKey(t), "lan")); code != http.StatusBadRequest || !strings.Contains(body, "invalid_grant") {
		t.Fatalf("exchange with a foreign key = %d %s; want 400 invalid_grant", code, body)
	}
	if code, _ := postExchange(t, r.api, tenantToken(t, st.tenantKey, "minh")); code != http.StatusOK || st.profiles.Load() != 1 {
		t.Fatalf("new user exchange = %d with %d profile writes; want 200 and one write", code, st.profiles.Load())
	}
	requireMetrics(t, r.admin,
		`chatim_identity_exchanges_total{result="ok"} 2`, `chatim_identity_exchanges_total{result="invalid"} 1`,
		`chatim_identity_exchanges_total{result="disabled"} 0`, `chatim_identity_exchanges_total{result="rate"} 0`,
		`chatim_identity_exchanges_total{result="error"} 1`,
		`chatim_identity_jit_writes_total{result="created"} 1`, `chatim_identity_jit_writes_total{result="updated"} 0`,
		`chatim_identity_jit_writes_total{result="conflict"} 0`, `chatim_identity_jit_writes_total{result="error"} 0`,
		"chatim_identity_trust_reload_errors_total 0", "chatim_identity_store_errors_total 0",
		`chatim_identity_jit_batch_docs_bucket{le="2"} 3`, "chatim_identity_jit_batch_docs_sum 5", "chatim_identity_jit_batch_docs_count 3",
	)
}

var trustReloadErrors = regexp.MustCompile(`chatim_identity_trust_reload_errors_total [1-9]`)

func TestTrustReloadErrorsKeepTheNodeReady(t *testing.T) {
	kek := newKEK(t)
	cfg := testConfig(kek)
	cfg.TrustReload = 20 * time.Millisecond
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, trustFake: trustFake{trustLoads: make(chan struct{}, 1), tenantKey: newTenantKey(t)}}
	r := runIdentity(t, cfg, st)
	r.logs.await(t, "identity ready")
	st.trustDown.Store(true)
	for range 3 {
		receive(t, st.trustLoads, "trust reload while mongodb is down")
	}
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
	if _, body := httpGet(t, r.admin, "/metrics"); !trustReloadErrors.MatchString(body) {
		t.Fatal("metrics lack the failed trust reloads")
	}
	if code, body := postExchange(t, r.api, tenantToken(t, st.tenantKey, "lan")); code != http.StatusOK {
		t.Fatalf("exchange on the kept snapshot = %d %s; want 200", code, body)
	}
}

func TestTrustEventWakesTheTrustReloaderAndShutdownStopsItBeforeTheStoreCloses(t *testing.T) {
	kek := newKEK(t)
	cfg := testConfig(kek)
	cfg.TrustReload = time.Minute
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, trustFake: trustFake{trustHung: make(chan struct{}, 1)},
		outage: outage{events: make(chan store.StreamEvent)}}
	r := runIdentity(t, cfg, st)
	r.logs.await(t, "identity ready")
	st.trustHang.Store(true)
	st.events <- store.StreamEvent{Kinds: []store.EventKind{store.EventTrust}, Token: store.StreamToken("t1")}
	receive(t, st.trustHung, "trust reload woken by the rev notice")
	r.cancel()
	if err := receive(t, r.done, "run return"); err != nil {
		t.Fatalf("Run = %v; want nil", err)
	}
	if closed, last := st.closedAt.Load(), st.lastTrust.Load(); closed <= last {
		t.Fatalf("store closed at step %d before the last trust read at step %d", closed, last)
	}
}
