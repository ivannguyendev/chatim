package api_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/api"
	"github.com/ivannguyendev/chatim/apps/identity/internal/exchange"
	"github.com/ivannguyendev/chatim/apps/identity/internal/guard"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	grantExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	typeJWT       = "urn:ietf:params:oauth:token-type:jwt"
	formType      = "application/x-www-form-urlencoded"
)

type exchangeCall struct {
	token, ip string
}

type fakeExchange struct {
	mu     sync.Mutex
	calls  []exchangeCall
	issued exchange.Issued
	err    error
}

func (f *fakeExchange) Exchange(_ context.Context, token, ip string) (exchange.Issued, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, exchangeCall{token: token, ip: ip})
	return f.issued, f.err
}

func (f *fakeExchange) seen() []exchangeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]exchangeCall(nil), f.calls...)
}

type exchangeRig struct {
	*harness
	fake  *fakeExchange
	stats *api.ExchangeStats
}

func newExchangeRig(t *testing.T, mods ...func(*api.Options)) *exchangeRig {
	t.Helper()
	r := &exchangeRig{
		fake:  &fakeExchange{issued: exchange.Issued{AccessToken: "chat-token", ExpiresAt: t0.Add(5 * time.Minute)}},
		stats: &api.ExchangeStats{},
	}
	r.harness = newHarness(t, append([]func(*api.Options){func(o *api.Options) {
		o.Exchange, o.ExchangeIPs, o.ExchangeStats = r.fake, guard.NewIPLimiter(1, 100, 1000), r.stats
	}}, mods...)...)
	return r
}

func exchangeForm(token string) string {
	return url.Values{"grant_type": {grantExchange}, "subject_token": {token}, "subject_token_type": {typeJWT}}.Encode()
}

func (r *exchangeRig) post(t *testing.T, body string, headers map[string]string) reply {
	t.Helper()
	h := map[string]string{"Content-Type": formType}
	maps.Copy(h, headers)
	return r.do(t, http.MethodPost, "/v1/token", body, h)
}

func (r *exchangeRig) counts() string {
	s := r.stats
	return fmt.Sprintf("ok=%d invalid=%d disabled=%d rate=%d error=%d",
		s.OK.Load(), s.Invalid.Load(), s.Disabled.Load(), s.Limited.Load(), s.Errors.Load())
}

func requireOAuthError(t *testing.T, got reply, status int, code string) {
	t.Helper()
	if got.StatusCode != status || !strings.Contains(string(got.Body), `"error":"`+code+`"`) {
		t.Fatalf("reply = %d %s; want %d %s", got.StatusCode, got.Body, status, code)
	}
	if got.Header.Get("Cache-Control") != "no-store" || got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v; want no-store json", got.Header)
	}
}

func TestExchangeGrantsAChatToken(t *testing.T) {
	r := newExchangeRig(t)
	got := r.post(t, exchangeForm("tenant-token"), nil)
	want := `{"access_token":"chat-token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":300}`
	if got.StatusCode != http.StatusOK || strings.TrimSpace(string(got.Body)) != want {
		t.Fatalf("reply = %d %s; want 200 %s", got.StatusCode, got.Body, want)
	}
	if got.Header.Get("Cache-Control") != "no-store" || got.Header.Get("Pragma") != "no-cache" {
		t.Fatalf("headers = %v; want no-store", got.Header)
	}
	if calls := r.fake.seen(); len(calls) != 1 || calls[0] != (exchangeCall{token: "tenant-token", ip: "127.0.0.1"}) {
		t.Fatalf("exchange calls = %+v; want the subject token from 127.0.0.1", calls)
	}
	if c := r.counts(); c != "ok=1 invalid=0 disabled=0 rate=0 error=0" {
		t.Fatalf("counts %s; want one ok", c)
	}
}

func TestExchangeRejectsMalformedRequestsWithoutCounting(t *testing.T) {
	valid := url.Values{"grant_type": {grantExchange}, "subject_token": {"t"}, "subject_token_type": {typeJWT}}
	with := func(change func(url.Values)) string {
		v := url.Values{}
		for k, vs := range valid {
			v[k] = append([]string(nil), vs...)
		}
		change(v)
		return v.Encode()
	}
	for _, tc := range []struct {
		name, body, contentType, code string
		status                        int
	}{
		{name: "json body", body: `{"grant_type":"x"}`, contentType: "application/json", code: "invalid_request", status: http.StatusBadRequest},
		{name: "no content type", body: exchangeForm("t"), contentType: "", code: "invalid_request", status: http.StatusBadRequest},
		{name: "refresh token grant", body: with(func(v url.Values) { v.Set("grant_type", "refresh_token") }), code: "unsupported_grant_type", status: http.StatusBadRequest},
		{name: "missing grant", body: with(func(v url.Values) { v.Del("grant_type") }), code: "invalid_request", status: http.StatusBadRequest},
		{name: "repeated subject token", body: with(func(v url.Values) { v.Add("subject_token", "u") }), code: "invalid_request", status: http.StatusBadRequest},
		{name: "empty subject token", body: with(func(v url.Values) { v.Set("subject_token", "") }), code: "invalid_request", status: http.StatusBadRequest},
		{name: "access token type", body: with(func(v url.Values) { v.Set("subject_token_type", "urn:ietf:params:oauth:token-type:access_token") }), code: "invalid_request", status: http.StatusBadRequest},
		{name: "missing token type", body: with(func(v url.Values) { v.Del("subject_token_type") }), code: "invalid_request", status: http.StatusBadRequest},
		{name: "body over the limit", body: "subject_token=" + strings.Repeat("a", 17<<10), code: "invalid_request", status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newExchangeRig(t)
			ct := formType + "; charset=utf-8"
			if tc.contentType != "" || tc.name == "no content type" {
				ct = tc.contentType
			}
			requireOAuthError(t, r.do(t, http.MethodPost, "/v1/token", tc.body, map[string]string{"Content-Type": ct}), tc.status, tc.code)
			if calls := r.fake.seen(); len(calls) != 0 {
				t.Fatalf("exchange called %+v; want no call", calls)
			}
			if c := r.counts(); c != "ok=0 invalid=0 disabled=0 rate=0 error=0" {
				t.Fatalf("counts %s; want none", c)
			}
		})
	}
}

func TestExchangeRefusalsLookTheSame(t *testing.T) {
	replies := map[string]reply{}
	for name, err := range map[string]error{"invalid": exchange.ErrRefused, "disabled": exchange.ErrDisabled} {
		r := newExchangeRig(t)
		r.fake.err = fmt.Errorf("wrapped: %w", err)
		got := r.post(t, exchangeForm("tenant-token"), nil)
		requireOAuthError(t, got, http.StatusBadRequest, "invalid_grant")
		if c, want := r.counts(), "ok=0 invalid=1 disabled=0 rate=0 error=0"; name == "invalid" && c != want {
			t.Fatalf("counts %s; want %s", c, want)
		}
		if c, want := r.counts(), "ok=0 invalid=0 disabled=1 rate=0 error=0"; name == "disabled" && c != want {
			t.Fatalf("counts %s; want %s", c, want)
		}
		replies[name] = got
	}
	a, b := replies["invalid"], replies["disabled"]
	if string(a.Body) != string(b.Body) {
		t.Fatalf("bodies differ: %s vs %s", a.Body, b.Body)
	}
	for _, h := range []string{"Content-Length", "Retry-After", "Content-Type", "Cache-Control"} {
		if a.Header.Get(h) != b.Header.Get(h) {
			t.Fatalf("%s differs: %q vs %q", h, a.Header.Get(h), b.Header.Get(h))
		}
	}
	if a.Header.Get("Content-Length") == "" || a.Header.Get("Retry-After") != "" {
		t.Fatalf("headers = %v; want a Content-Length and no Retry-After", a.Header)
	}
}

func TestExchangeUnavailableIs503(t *testing.T) {
	r := newExchangeRig(t)
	r.fake.err = fmt.Errorf("%w: no signing key", apperr.ErrUnavailable)
	got := r.post(t, exchangeForm("tenant-token"), nil)
	requireOAuthError(t, got, http.StatusServiceUnavailable, "temporarily_unavailable")
	if got.Header.Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q; want 1", got.Header.Get("Retry-After"))
	}
	if c := r.counts(); c != "ok=0 invalid=0 disabled=0 rate=0 error=1" {
		t.Fatalf("counts %s; want one error", c)
	}
	r.fake.err = errors.New("plain failure")
	requireOAuthError(t, r.post(t, exchangeForm("tenant-token"), nil), http.StatusServiceUnavailable, "temporarily_unavailable")
	if strings.Contains(r.logs.String(), "plain failure") {
		t.Fatalf("handler logged %q; the exchange use case logs every 503 cause once", r.logs.String())
	}
}

func TestExchangeLimitsEachClientIP(t *testing.T) {
	r := newExchangeRig(t, func(o *api.Options) { o.ExchangeIPs = guard.NewIPLimiter(1, 2, 1000) })
	for range 2 {
		if got := r.post(t, exchangeForm("t"), nil); got.StatusCode != http.StatusOK {
			t.Fatalf("within burst = %d %s; want 200", got.StatusCode, got.Body)
		}
	}
	got := r.post(t, exchangeForm("t"), nil)
	requireOAuthError(t, got, http.StatusTooManyRequests, "temporarily_unavailable")
	if got.Header.Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q; want 1", got.Header.Get("Retry-After"))
	}
	if c := r.counts(); c != "ok=2 invalid=0 disabled=0 rate=1 error=0" {
		t.Fatalf("counts %s; want two ok and one rate", c)
	}
	if got := r.post(t, "grant_type=refresh_token", nil); got.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad grant from a limited ip = %d; want 400 before the limiter", got.StatusCode)
	}
}

func TestExchangeTrustsTheClientIPHeaderOnlyWhenConfigured(t *testing.T) {
	for _, tc := range []struct {
		header string
		wantIP string
	}{{header: "", wantIP: "127.0.0.1"}, {header: "X-Forwarded-For", wantIP: "198.51.100.7"}} {
		r := newExchangeRig(t, func(o *api.Options) {
			o.ClientIPHeader = tc.header
			o.ExchangeIPs = guard.NewIPLimiter(1, 1, 1000)
		})
		first := r.post(t, exchangeForm("t"), map[string]string{"X-Forwarded-For": "203.0.113.1, 198.51.100.7"})
		second := r.post(t, exchangeForm("t"), map[string]string{"X-Forwarded-For": "203.0.113.1, 198.51.100.8"})
		if first.StatusCode != http.StatusOK {
			t.Fatalf("header %q: first = %d; want 200", tc.header, first.StatusCode)
		}
		wantSecond := http.StatusTooManyRequests
		if tc.header != "" {
			wantSecond = http.StatusOK
		}
		if second.StatusCode != wantSecond {
			t.Fatalf("header %q: second = %d; want %d", tc.header, second.StatusCode, wantSecond)
		}
		if calls := r.fake.seen(); calls[0].ip != tc.wantIP {
			t.Fatalf("header %q: exchange ip = %q; want %q", tc.header, calls[0].ip, tc.wantIP)
		}
	}
}

func TestExchangePreflightAndMethods(t *testing.T) {
	r := newExchangeRig(t)
	got := r.do(t, http.MethodOptions, "/v1/token", "", map[string]string{
		"Origin": "https://app.example.com", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "Content-Type",
	})
	if got.StatusCode != http.StatusNoContent || got.Header.Get("Access-Control-Allow-Origin") != "https://app.example.com" ||
		!strings.Contains(got.Header.Get("Access-Control-Allow-Headers"), "Content-Type") {
		t.Fatalf("preflight = %d %v; want 204 for the listed origin", got.StatusCode, got.Header)
	}
	if got := r.do(t, http.MethodOptions, "/v1/token", "", map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "POST"}); got.StatusCode != http.StatusForbidden {
		t.Fatalf("preflight from an unlisted origin = %d; want 403", got.StatusCode)
	}
	if got := r.do(t, http.MethodGet, "/v1/token", "", nil); got.StatusCode != http.StatusNotFound || len(r.fake.seen()) != 0 {
		t.Fatalf("GET /v1/token = %d with %d exchanges; want 404 and none", got.StatusCode, len(r.fake.seen()))
	}
}

func TestExchangeNeedsAnIPLimiter(t *testing.T) {
	if _, err := api.New(api.Options{Keys: fakeKeys{}, BodyMax: 1, Exchange: &fakeExchange{}}); err == nil {
		t.Fatal("api.New accepted an exchange without an ip limiter")
	}
}

func TestExchangeExpiresInRoundsUpTheRemainingSeconds(t *testing.T) {
	for _, tc := range []struct {
		left time.Duration
		want string
	}{{left: 5*time.Minute - 300*time.Millisecond, want: `"expires_in":300`}, {left: -time.Second, want: `"expires_in":0`}} {
		r := newExchangeRig(t)
		r.fake.issued.ExpiresAt = t0.Add(tc.left)
		if got := r.post(t, exchangeForm("t"), nil); !strings.Contains(string(got.Body), tc.want) {
			t.Fatalf("%v left: reply %s; want %s", tc.left, got.Body, tc.want)
		}
	}
}
