package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func nodeHeader() http.Header { return http.Header{"Authorization": {"Bearer " + testNodeSecret}} }

func TestBothPortsServeTheirOwnRoutes(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3,
		snapshot: revocation.Snapshot{Sessions: []revocation.Session{{ID: "iphone", Tenant: "shop", ExpiresAt: time.Now().Add(time.Minute)}}}}
	r := runIAM(t, testConfig(kek), st, 3)
	r.logs.await(t, "iam ready")
	if code, body := httpGetWith(t, r.internal, "/internal/v1/revocations", nodeHeader()); code != http.StatusOK || !strings.Contains(body, "iphone") {
		t.Fatalf("internal revocations = %d %s", code, body)
	}
	if code, _ := httpGetWith(t, r.api, "/internal/v1/revocations", nodeHeader()); code != http.StatusNotFound {
		t.Fatalf("revocations on the public port = %d; want 404", code)
	}
	if code, body := httpGet(t, r.api, "/.well-known/jwks.json"); code != http.StatusOK || !strings.Contains(body, st.keys[0].ID) {
		t.Fatalf("public jwks = %d %s", code, body)
	}
	if code, _ := httpGet(t, r.internal, "/.well-known/jwks.json"); code != http.StatusNotFound {
		t.Fatalf("jwks on the internal port = %d; want 404", code)
	}
	if code, body := httpGet(t, r.admin, "/metrics"); code != http.StatusOK || !strings.Contains(body, `chatim_iam_revocation_requests_total{result="ok"} 1`) {
		t.Fatalf("metrics = %d, missing revocation request series", code)
	}
}

func TestEachPortRunsAlone(t *testing.T) {
	kek := newKEK(t)
	t.Run("internal only", func(t *testing.T) {
		cfg := testConfig(kek)
		cfg.ListenAddr, cfg.PublicURL = "", ""
		r := runIAM(t, cfg, &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3}, 3)
		r.logs.await(t, "iam ready")
		if code, _ := httpGetWith(t, r.internal, "/internal/v1/revocations", nodeHeader()); code != http.StatusOK {
			t.Fatalf("internal = %d", code)
		}
	})
	t.Run("public only", func(t *testing.T) {
		cfg := testConfig(kek)
		cfg.InternalAddr, cfg.NodeSecret = "", ""
		r := runIAM(t, cfg, &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3}, 3)
		r.logs.await(t, "iam ready")
		if code, _ := httpGet(t, r.api, "/.well-known/jwks.json"); code != http.StatusOK {
			t.Fatalf("public jwks = %d", code)
		}
	})
}

func TestExpiredRevocationsAreCleanedOnTheReloadTick(t *testing.T) {
	kek := newKEK(t)
	cfg := testConfig(kek)
	cfg.KeyReload = 20 * time.Millisecond
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, cleaned: make(chan struct{}, 1)}
	r := runIAM(t, cfg, st, 3)
	receive(t, st.cleaned, "revocation cleanup")
	r.logs.await(t, "iam ready")
}

func TestDueKeyIsActivatedOnTheReloadTick(t *testing.T) {
	kek := newKEK(t)
	cfg := testConfig(kek)
	cfg.KeyReload = 20 * time.Millisecond
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, version: 3, due: "k-next", dueAsked: make(chan time.Duration, 1)}
	r := runIAM(t, cfg, st, 3)
	if lead := receive(t, st.dueAsked, "due key check"); lead != cfg.KeyActivateLead {
		t.Fatalf("lead = %v, want %v", lead, cfg.KeyActivateLead)
	}
	r.logs.await(t, "signing key activated")
	if _, body := httpGet(t, r.admin, "/metrics"); !strings.Contains(body, "chatim_iam_signing_keys_activated_total 1") {
		t.Fatalf("/metrics lacks the activation: %s", body)
	}
}
