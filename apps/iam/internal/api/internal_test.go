package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

const nodeSecret = "node-secret-value-0123456789abcdef"

type fakeRevocations struct {
	mu   sync.Mutex
	snap revocation.Snapshot
	err  error
}

func (f *fakeRevocations) Revocations(context.Context) (revocation.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap, f.err
}

func (f *fakeRevocations) set(snap revocation.Snapshot, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snap, f.err = snap, err
}

type internalHarness struct {
	src    *fakeRevocations
	stats  *api.InternalStats
	server *httptest.Server
}

func newInternal(t *testing.T) *internalHarness {
	t.Helper()
	src := &fakeRevocations{snap: revocation.Snapshot{Sessions: []revocation.Session{{ID: "iphone", Tenant: "shop", ExpiresAt: t0}}}}
	stats := &api.InternalStats{}
	h, err := api.NewInternal(api.InternalOptions{Revocations: src, Secret: nodeSecret, Stats: stats})
	if err != nil {
		t.Fatalf("NewInternal: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &internalHarness{src: src, stats: stats, server: srv}
}

func (h *internalHarness) get(t *testing.T, path string, headers map[string]string) reply {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.server.URL+path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return reply{StatusCode: resp.StatusCode, Header: resp.Header, Body: body}
}

func bearer(secret string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + secret}
}

func TestNewInternalNeedsASourceAndASecret(t *testing.T) {
	if _, err := api.NewInternal(api.InternalOptions{Secret: nodeSecret}); err == nil {
		t.Fatal("accepted a missing source")
	}
	if _, err := api.NewInternal(api.InternalOptions{Revocations: &fakeRevocations{}}); err == nil {
		t.Fatal("accepted a missing secret")
	}
}

func TestRevocationsNeedTheNodeSecret(t *testing.T) {
	h := newInternal(t)
	for name, headers := range map[string]map[string]string{
		"missing":    nil,
		"wrong":      bearer("node-secret-value-0123456789abcdeX"),
		"short":      bearer("x"),
		"not bearer": {"Authorization": "Basic " + nodeSecret},
	} {
		r := h.get(t, "/internal/v1/revocations", headers)
		if r.StatusCode != http.StatusUnauthorized || strings.Contains(string(r.Body), "iphone") {
			t.Fatalf("%s secret = %d %s; want 401 without the list", name, r.StatusCode, r.Body)
		}
	}
	if h.stats.Unauthorized.Load() != 4 {
		t.Fatalf("unauthorized = %d; want 4", h.stats.Unauthorized.Load())
	}
}

func TestRevocationsServeTheListWithAnETag(t *testing.T) {
	h := newInternal(t)
	first := h.get(t, "/internal/v1/revocations", bearer(nodeSecret))
	if first.StatusCode != http.StatusOK || first.Header.Get("Content-Type") != "application/json" || first.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("first = %d %v", first.StatusCode, first.Header)
	}
	var snap revocation.Snapshot
	if err := json.Unmarshal(first.Body, &snap); err != nil || len(snap.Sessions) != 1 || snap.Sessions[0].ID != "iphone" {
		t.Fatalf("body = %s (%v)", first.Body, err)
	}
	tag := first.Header.Get("ETag")
	if !strings.HasPrefix(tag, `"`) || len(tag) < 10 {
		t.Fatalf("etag = %q", tag)
	}
	same := h.get(t, "/internal/v1/revocations", map[string]string{"Authorization": "Bearer " + nodeSecret, "If-None-Match": tag})
	if same.StatusCode != http.StatusNotModified || len(same.Body) != 0 {
		t.Fatalf("unchanged = %d %q; want 304 without a body", same.StatusCode, same.Body)
	}
	h.src.set(revocation.Snapshot{Tenants: []revocation.Tenant{{ID: "shop", RevokedBefore: t0, ExpiresAt: t0.Add(time.Minute)}}}, nil)
	changed := h.get(t, "/internal/v1/revocations", map[string]string{"Authorization": "Bearer " + nodeSecret, "If-None-Match": tag})
	if changed.StatusCode != http.StatusOK || changed.Header.Get("ETag") == tag {
		t.Fatalf("changed = %d etag %q", changed.StatusCode, changed.Header.Get("ETag"))
	}
	if h.stats.OK.Load() != 2 || h.stats.NotModified.Load() != 1 {
		t.Fatalf("stats ok %d not modified %d", h.stats.OK.Load(), h.stats.NotModified.Load())
	}
}

func TestRevocationsStoreErrorIsRetryable(t *testing.T) {
	h := newInternal(t)
	h.src.set(revocation.Snapshot{}, errors.New("postgres down"))
	r := h.get(t, "/internal/v1/revocations", bearer(nodeSecret))
	if r.StatusCode != http.StatusServiceUnavailable || r.Header.Get("Retry-After") != "1" || strings.Contains(string(r.Body), "postgres") {
		t.Fatalf("store error = %d %v %s", r.StatusCode, r.Header, r.Body)
	}
	if h.stats.Errors.Load() != 1 {
		t.Fatalf("errors = %d", h.stats.Errors.Load())
	}
}

func TestInternalPortServesNothingElse(t *testing.T) {
	h := newInternal(t)
	for _, path := range []string{"/.well-known/jwks.json", "/realms/shop/protocol/openid-connect/token", "/chatim.iam.v1.AuthService/Login", "/"} {
		if r := h.get(t, path, bearer(nodeSecret)); r.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s on the internal port = %d; want 404", path, r.StatusCode)
		}
	}
}

func TestPublicPortServesJWKSButNotTheRevocationList(t *testing.T) {
	h := newHarness(t)
	jwks := h.do(t, http.MethodGet, "/.well-known/jwks.json", "", nil)
	if jwks.StatusCode != http.StatusOK || string(jwks.Body) != testJWKS || jwks.Header.Get("Content-Type") != "application/jwk-set+json" || jwks.Header.Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("jwks = %d %v %s", jwks.StatusCode, jwks.Header, jwks.Body)
	}
	if r := h.do(t, http.MethodGet, "/internal/v1/revocations", "", bearer(nodeSecret)); r.StatusCode != http.StatusNotFound {
		t.Fatalf("revocation list on the public port = %d; want 404", r.StatusCode)
	}
}

func TestPublicJWKSWithoutKeysIsUnavailable(t *testing.T) {
	h, err := api.New(api.Options{Auth: &fakeAuth{}, Proofs: fakeProofs{}, Callers: fakeCallers{}, Keys: fakeKeys{}, BodyMax: 1 << 10})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("jwks without keys = %d; want 503", rec.Code)
	}
}
