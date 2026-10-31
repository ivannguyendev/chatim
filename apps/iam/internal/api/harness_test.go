package api_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/dpop"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var t0 = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

const (
	secretPassword = "correct horse battery"
	secretRefresh  = "aaaaaaaaaaaaaaaaaaaaaaaaaa.REFRESHSECRETREFRESHSECRETREFRESHSECRETABC"
	secretAccess   = "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJsYW4ifQ.c2lnbmF0dXJl"
)

type fakeAuth struct {
	mu        sync.Mutex
	err       error
	logins    []auth.LoginRequest
	refreshes []auth.RefreshRequest
	revokes   []auth.RevokeRequest
	callers   []auth.Caller
	revoked   string
	profiles  profileFake
}

func (f *fakeAuth) Login(_ context.Context, r auth.LoginRequest) (auth.Tokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins = append(f.logins, r)
	if f.err != nil {
		return auth.Tokens{}, f.err
	}
	return auth.Tokens{AccessToken: secretAccess, ExpiresAt: t0.Add(15 * time.Minute), RefreshToken: secretRefresh, SessionID: "aaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil
}

func (f *fakeAuth) ChangePassword(_ context.Context, _ auth.ChangePasswordRequest) error {
	return f.err
}

func (f *fakeAuth) Refresh(_ context.Context, r auth.RefreshRequest) (auth.Tokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes = append(f.refreshes, r)
	if f.err != nil {
		return auth.Tokens{}, f.err
	}
	return auth.Tokens{AccessToken: secretAccess, ExpiresAt: t0.Add(15 * time.Minute), RefreshToken: secretRefresh, SessionID: "aaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil
}

func (f *fakeAuth) Revoke(_ context.Context, r auth.RevokeRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokes = append(f.revokes, r)
	return f.err
}

func (f *fakeAuth) ListSessions(_ context.Context, c auth.Caller) ([]auth.SessionView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callers = append(f.callers, c)
	if f.err != nil {
		return nil, f.err
	}
	return []auth.SessionView{{ID: c.Session, DeviceName: "iPhone", CreatedAt: t0, LastUsedAt: t0, Current: true}}, nil
}

func (f *fakeAuth) RevokeSession(_ context.Context, c auth.Caller, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callers = append(f.callers, c)
	f.revoked = id
	return f.err
}

type fakeProofs struct{}

func (fakeProofs) Verify(proof, method, path string) (dpop.Proof, error) {
	if proof != "good-proof" || method != http.MethodPost {
		return dpop.Proof{}, dpop.ErrInvalidProof
	}
	return dpop.Proof{Thumbprint: "jkt-" + path, JTI: "jti-1"}, nil
}

type fakeCallers struct{}

func (fakeCallers) Caller(token string) (auth.Caller, error) {
	if token != "good-access" {
		return auth.Caller{}, errors.Join(apperr.ErrUnauthenticated, errors.New("bad token"))
	}
	return auth.Caller{Tenant: "shop", Account: "lan", Session: "s1"}, nil
}

type fakeKeys struct{ body []byte }

func (f fakeKeys) JWKS() ([]byte, bool) { return f.body, f.body != nil }

const testJWKS = `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k-1","x":"abc"}]}`

type harness struct {
	fake   *fakeAuth
	server *httptest.Server
	logs   *bytes.Buffer
}

func newHarness(t *testing.T, mods ...func(*api.Options)) *harness {
	t.Helper()
	logs := &bytes.Buffer{}
	fake := &fakeAuth{}
	opts := api.Options{
		Auth: fake, Proofs: fakeProofs{}, Callers: fakeCallers{}, Keys: fakeKeys{body: []byte(testJWKS)},
		BodyMax: 16 << 10, CORSOrigins: []string{"https://app.example.com"},
		ClientIPHeader: "X-Real-IP", Now: func() time.Time { return t0 },
		Log: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	for _, mod := range mods {
		mod(&opts)
	}
	h, err := api.New(opts)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &harness{fake: fake, server: srv, logs: logs}
}

type reply struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func (h *harness) do(t *testing.T, method, path, body string, headers map[string]string) reply {
	t.Helper()
	extra := http.Header{}
	for k, v := range headers {
		extra.Set(k, v)
	}
	return h.doHeaders(t, method, path, body, extra)
}

func (h *harness) doHeaders(t *testing.T, method, path, body string, headers http.Header) reply {
	t.Helper()
	req, _ := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, values := range headers {
		req.Header.Del(k)
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return reply{StatusCode: resp.StatusCode, Header: resp.Header, Body: b}
}

func (h *harness) postForm(t *testing.T, path, body string, headers map[string]string) reply {
	t.Helper()
	return h.do(t, http.MethodPost, path, body, headers)
}

func (h *harness) assertNoSecretsLogged(t *testing.T) {
	t.Helper()
	for _, secret := range []string{secretPassword, secretRefresh, secretAccess, "good-proof", "good-access"} {
		if strings.Contains(h.logs.String(), secret) {
			t.Fatalf("log contains a secret %q", secret)
		}
	}
}
