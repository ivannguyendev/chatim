package api_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/v1/chatimv1connect"
)

const historyPath = chatimv1.CoreService_GetHistory_FullMethodName

var historyJSON = []byte(`{"roomId":"42"}`)

func TestBadTokensGet401BeforeAnyCoreCall(t *testing.T) {
	h := newHarness(t, roomy)
	good := h.token(t, claims{})
	other := &harness{}
	_, other.priv, _ = ed25519.GenerateKey(rand.Reader)
	cases := []struct {
		name, reason string
		header       []string
	}{
		{"missing", "missing", nil},
		{"basic scheme", "bad header", []string{"Basic " + good}},
		{"no token", "bad header", []string{"Bearer"}},
		{"unicode", "bad header", []string{"Bearer tökén"}},
		{"too long", "too long", []string{"Bearer " + strings.Repeat("a", 9000)}},
		{"repeated", "repeated", []string{bearerOf(good), bearerOf(good)}},
		{"malformed", "malformed", []string{"Bearer abc"}},
		{"expired", "expired", []string{bearerOf(h.token(t, claims{expires: time.Now().Add(-time.Hour)}))}},
		{"wrong issuer", "bad issuer", []string{bearerOf(h.token(t, claims{issuer: "someone"}))}},
		{"wrong audience", "bad audience", []string{bearerOf(h.token(t, claims{audience: "chatim-websocket"}))}},
		{"wrong key", "bad signature", []string{bearerOf(other.token(t, claims{}))}},
	}
	for _, tc := range cases {
		resp := h.post(t, historyPath, "application/json", "", historyJSON, http.Header{"Authorization": tc.header})
		if resp.status != http.StatusUnauthorized || errorCode(t, resp) != "unauthenticated" {
			t.Fatalf("%s status = %d", tc.name, resp.status)
		}
		if got := h.read(t, "auth_failures_total", map[string]string{"reason": tc.reason}); got < 1 {
			t.Fatalf("%s reason %q not counted", tc.name, tc.reason)
		}
	}
	if len(h.core.recorded()) != 0 {
		t.Fatal("a refused request reached core")
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "gateway auth failed") || strings.Contains(logs, good) || strings.Contains(logs, "Authorization") {
		t.Fatalf("logs = %s", logs)
	}
}

func TestPerUserRateKeepsUsersAndTenantsApart(t *testing.T) {
	h := newHarness(t, limits{rate: 1, burst: 2, inflight: 32, node: 100})
	alice := bearerOf(h.token(t, claims{tenant: "t1", user: "alice"}))
	for range 2 {
		if resp := h.post(t, historyPath, "application/json", alice, historyJSON, nil); resp.status != http.StatusOK {
			t.Fatalf("status = %d", resp.status)
		}
	}
	resp := h.post(t, historyPath, "application/json", alice, historyJSON, nil)
	if resp.status != http.StatusTooManyRequests || errorCode(t, resp) != "resource_exhausted" {
		t.Fatalf("third = %d", resp.status)
	}
	for _, c := range []claims{{tenant: "t1", user: "bob"}, {tenant: "t2", user: "alice"}} {
		if resp := h.post(t, historyPath, "application/json", bearerOf(h.token(t, c)), historyJSON, nil); resp.status != http.StatusOK {
			t.Fatalf("%+v status = %d", c, resp.status)
		}
	}
	if got := h.read(t, "rate_limited_total", map[string]string{"limit": "rate"}); got != 1 {
		t.Fatalf("rate limited = %v", got)
	}
}

func hangCore(h *harness) (started, release chan struct{}) {
	started, release = make(chan struct{}, 4), make(chan struct{})
	h.core.set(func(f *fakeCore) { f.started, f.release = started, release })
	return started, release
}

func TestUserInflightAndNodeGateRefuseWhileACallHangs(t *testing.T) {
	for name, l := range map[string]limits{"user": {rate: 100, burst: 100, inflight: 1, node: 100}, "node": {rate: 100, burst: 100, inflight: 32, node: 1}} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, l)
			started, release := hangCore(h)
			token := bearerOf(h.token(t, claims{}))
			first := make(chan int, 1)
			go func() {
				req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, h.server.URL+historyPath, bytes.NewReader(historyJSON))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", token)
				resp, err := h.server.Client().Do(req)
				if err != nil {
					first <- 0
					return
				}
				_ = resp.Body.Close()
				first <- resp.StatusCode
			}()
			<-started
			resp := h.post(t, historyPath, "application/json", token, historyJSON, nil)
			wantStatus, wantCode, metric, labels := http.StatusTooManyRequests, "resource_exhausted", "rate_limited_total", map[string]string{"limit": "inflight"}
			if name == "node" {
				wantStatus, wantCode, metric, labels = http.StatusServiceUnavailable, "unavailable", "inflight_rejected_total", nil
			}
			if resp.status != wantStatus || errorCode(t, resp) != wantCode {
				t.Fatalf("second status = %d", resp.status)
			}
			if got := h.read(t, metric, labels); got != 1 {
				t.Fatalf("%s = %v", metric, got)
			}
			close(release)
			if got := <-first; got != http.StatusOK {
				t.Fatalf("first status = %d", got)
			}
		})
	}
}

func TestOversizeAndCompressedBodiesNeverReachCore(t *testing.T) {
	h := newHarness(t, roomy)
	token := bearerOf(h.token(t, claims{}))
	big := []byte(`{"roomId":"` + strings.Repeat("4", 5000) + `"}`)
	resp := h.post(t, historyPath, "application/json", token, big, nil)
	if code := errorCode(t, resp); code != "resource_exhausted" {
		t.Fatalf("oversize code = %q", code)
	}
	if got := h.read(t, "body_too_large_total", nil); got != 1 {
		t.Fatalf("body too large = %v", got)
	}
	gz := h.post(t, historyPath, "application/json", token, historyJSON, http.Header{"Content-Encoding": {"gzip"}})
	if gz.status == http.StatusOK {
		t.Fatal("a gzip body was accepted")
	}
	if len(h.core.recorded()) != 0 {
		t.Fatal("an oversize or compressed body reached core")
	}
}

func preflight(t *testing.T, h *harness, origin string) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, h.server.URL+historyPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type,connect-protocol-version")
	return h.do(t, req)
}

func TestCORSPreflightNeedsNoTokenAndOnlyListedOrigins(t *testing.T) {
	h := newHarness(t, roomy)
	ok := preflight(t, h, testOrigin)
	want := map[string]string{
		"Access-Control-Allow-Origin":  testOrigin,
		"Access-Control-Allow-Methods": "POST",
		"Access-Control-Allow-Headers": "Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms",
		"Access-Control-Max-Age":       "600",
	}
	for k, v := range want {
		if got := ok.header.Get(k); got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
	if ok.status != http.StatusNoContent || ok.header.Get("Access-Control-Allow-Credentials") != "" || !strings.Contains(strings.Join(ok.header.Values("Vary"), ","), "Origin") {
		t.Fatalf("preflight = %d %v", ok.status, ok.header)
	}
	denied := preflight(t, h, "https://evil.example.com")
	if denied.status != http.StatusForbidden || denied.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("denied preflight = %d %v", denied.status, denied.header)
	}
	if got := h.read(t, "auth_failures_total", map[string]string{"reason": "missing"}); got != 0 {
		t.Fatalf("a preflight went through auth: %v", got)
	}
	token := bearerOf(h.token(t, claims{}))
	allowed := h.post(t, historyPath, "application/json", token, historyJSON, http.Header{"Origin": {testOrigin}})
	if allowed.header.Get("Access-Control-Allow-Origin") != testOrigin || allowed.header.Get("Access-Control-Expose-Headers") == "" || allowed.header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("allowed request headers = %v", allowed.header)
	}
	other := h.post(t, historyPath, "application/json", token, historyJSON, http.Header{"Origin": {"https://evil.example.com"}})
	if other.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("other origin headers = %v", other.header)
	}
}

func TestMetricSourcesArePrebuilt(t *testing.T) {
	h := newHarness(t, roomy)
	names := map[string]int{}
	for _, s := range h.handler.Sources() {
		names[s.Name]++
	}
	if names["auth_failures_total"] != 22 || names["rate_limited_total"] != 2 || names["inflight_rejected_total"] != 1 || names["body_too_large_total"] != 1 || names["commands_total"] != 1 || names["inflight"] != 1 {
		t.Fatalf("sources = %v", names)
	}
	client := chatimv1connect.NewCoreServiceClient(h.server.Client(), h.server.URL, withToken(h.token(t, claims{})))
	if _, err := client.GetHistory(t.Context(), connect.NewRequest(&chatimv1.GetHistoryRequest{RoomId: "42"})); err != nil {
		t.Fatal(err)
	}
	if got := h.read(t, "inflight", nil); got != 0 {
		t.Fatalf("inflight after the call = %v", got)
	}
}
