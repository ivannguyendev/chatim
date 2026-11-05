package api_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/identity/internal/api"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var t0 = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

type fakeKeys struct{ body []byte }

func (f fakeKeys) JWKS() ([]byte, bool) { return f.body, f.body != nil }

const testJWKS = `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k-1","x":"abc"}]}`

type harness struct {
	server *httptest.Server
	logs   *bytes.Buffer
}

func newHarness(t *testing.T, mods ...func(*api.Options)) *harness {
	t.Helper()
	logs := &bytes.Buffer{}
	opts := api.Options{
		Keys: fakeKeys{body: []byte(testJWKS)}, BodyMax: 16 << 10, CORSOrigins: []string{"https://app.example.com"},
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
	return &harness{server: srv, logs: logs}
}

type reply struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func (h *harness) do(t *testing.T, method, path, body string, headers map[string]string) reply {
	t.Helper()
	req, _ := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
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
