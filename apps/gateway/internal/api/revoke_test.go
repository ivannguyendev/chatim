package api_test

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

type fakeRevocations struct {
	list    *revocation.List
	unknown atomic.Bool
}

func newFakeRevocations(t *testing.T) *fakeRevocations {
	t.Helper()
	list, err := revocation.New(revocation.Config{Leeway: time.Second, MaxKeep: time.Hour, MaxAhead: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return &fakeRevocations{list: list}
}

func (f *fakeRevocations) Check(c jwtverify.Claims) revocation.Verdict { return f.list.Check(c) }

func (f *fakeRevocations) UnknownKey(string) bool { return f.unknown.Load() }

func TestRevokedTokensGet401(t *testing.T) {
	h := newHarness(t, roomy)
	now := time.Now()
	h.revoked.list.Merge(revocation.Snapshot{Sessions: []revocation.Session{{ID: "phone", Tenant: "t1", ExpiresAt: now.Add(16 * time.Minute)}}}, now)
	resp := h.post(t, historyPath, "application/json", "", historyJSON, http.Header{"Authorization": {bearerOf(h.token(t, claims{session: "phone"}))}})
	if resp.status != http.StatusUnauthorized || errorCode(t, resp) != "unauthenticated" || h.read(t, "auth_failures_total", map[string]string{"reason": "revoked"}) != 1 {
		t.Fatalf("revoked session status = %d", resp.status)
	}
	h.revoked.list.Merge(revocation.Snapshot{Tenants: []revocation.Tenant{{ID: "t2", RevokedBefore: now, ExpiresAt: now.Add(16 * time.Minute)}}}, now)
	resp = h.post(t, historyPath, "application/json", "", historyJSON, http.Header{"Authorization": {bearerOf(h.token(t, claims{tenant: "t2", expires: now.Add(30 * time.Minute)}))}})
	if resp.status != http.StatusUnauthorized || h.read(t, "auth_failures_total", map[string]string{"reason": "tenant_revoked"}) != 1 {
		t.Fatalf("revoked tenant status = %d", resp.status)
	}
	if len(h.core.recorded()) != 0 {
		t.Fatal("a revoked token reached core")
	}
	ok := h.post(t, historyPath, "application/json", "", historyJSON, http.Header{"Authorization": {bearerOf(h.token(t, claims{session: "laptop"}))}})
	if ok.status != http.StatusOK {
		t.Fatalf("another session status = %d", ok.status)
	}
}

func TestUnknownKeyIsRetryableUntilKeysAreFetched(t *testing.T) {
	h := newHarness(t, roomy)
	token := bearerOf(h.token(t, claims{kid: "k-new"}))
	h.revoked.unknown.Store(true)
	resp := h.post(t, historyPath, "application/json", "", historyJSON, http.Header{"Authorization": {token}})
	if resp.status != http.StatusServiceUnavailable || errorCode(t, resp) != "unavailable" || h.read(t, "auth_failures_total", map[string]string{"reason": "key_pending"}) != 1 {
		t.Fatalf("pending key status = %d", resp.status)
	}
	h.revoked.unknown.Store(false)
	resp = h.post(t, historyPath, "application/json", "", historyJSON, http.Header{"Authorization": {token}})
	if resp.status != http.StatusUnauthorized || h.read(t, "auth_failures_total", map[string]string{"reason": "unknown kid"}) != 1 {
		t.Fatalf("unknown key status = %d", resp.status)
	}
}
