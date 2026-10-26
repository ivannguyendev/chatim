package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const tokenPath = "/realms/shop/protocol/openid-connect/token"
const revokePath = "/realms/shop/protocol/openid-connect/revoke"

func refreshBody(token string) string {
	return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}, "client_id": {"chatim-web"}}.Encode()
}

type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

func readError(t *testing.T, r reply) oauthError {
	t.Helper()
	var e oauthError
	if err := json.Unmarshal(r.Body, &e); err != nil {
		t.Fatalf("decode error body %q: %v", r.Body, err)
	}
	return e
}

func TestRefreshReturnsBearerTokensWithNoStore(t *testing.T) {
	h := newHarness(t)
	resp := h.postForm(t, tokenPath, refreshBody(secretRefresh), map[string]string{"DPoP": "good-proof", "X-Real-IP": "198.51.100.7"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Pragma") != "no-cache" {
		t.Fatalf("cache headers = %v", resp.Header)
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TokenType != "Bearer" || body.ExpiresIn != 900 || body.AccessToken != secretAccess || body.RefreshToken != secretRefresh {
		t.Fatalf("body = %+v", body)
	}
	got := h.fake.refreshes[0]
	if got.Tenant != "shop" || got.ClientID != "chatim-web" || got.IP != "198.51.100.7" || got.Proof.Thumbprint != "jkt-"+tokenPath {
		t.Fatalf("refresh request = %+v", got)
	}
	h.assertNoSecretsLogged(t)
}

func TestTokenEndpointErrors(t *testing.T) {
	cases := map[string]struct {
		body        string
		contentType string
		dpop        string
		authErr     error
		status      int
		code        string
	}{
		"wrong grant":         {url.Values{"grant_type": {"password"}}.Encode(), "", "good-proof", nil, 400, "unsupported_grant_type"},
		"missing grant":       {"refresh_token=x", "", "good-proof", nil, 400, "invalid_request"},
		"missing token":       {"grant_type=refresh_token", "", "good-proof", nil, 400, "invalid_request"},
		"json body":           {`{"grant_type":"refresh_token"}`, "application/json", "good-proof", nil, 400, "invalid_request"},
		"missing dpop":        {refreshBody("x"), "", "", nil, 400, "invalid_dpop_proof"},
		"bad dpop":            {refreshBody("x"), "", "bad-proof", nil, 400, "invalid_dpop_proof"},
		"replayed proof":      {refreshBody("x"), "", "good-proof", auth.ErrProofReplayed, 400, "invalid_dpop_proof"},
		"invalid grant":       {refreshBody("x"), "", "good-proof", auth.ErrInvalidGrant, 400, "invalid_grant"},
		"store unavailable":   {refreshBody("x"), "", "good-proof", apperr.ErrUnavailable, 503, "server_error"},
		"oversized body":      {refreshBody(strings.Repeat("x", 20<<10)), "", "good-proof", nil, 413, "invalid_request"},
		"duplicated grant":    {"grant_type=refresh_token&grant_type=refresh_token&refresh_token=x", "", "good-proof", nil, 400, "invalid_request"},
		"unknown sid in path": {refreshBody("x"), "", "good-proof", auth.ErrInvalidGrant, 400, "invalid_grant"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.fake.err = c.authErr
			headers := map[string]string{}
			if c.dpop != "" {
				headers["DPoP"] = c.dpop
			}
			if c.contentType != "" {
				headers["Content-Type"] = c.contentType
			}
			resp := h.postForm(t, tokenPath, c.body, headers)
			if resp.StatusCode != c.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, c.status)
			}
			if e := readError(t, resp); e.Error != c.code {
				t.Fatalf("error = %q, want %q", e.Error, c.code)
			}
			if c.status == 503 && resp.Header.Get("Retry-After") != "1" {
				t.Fatal("503 without Retry-After")
			}
		})
	}
}

func TestEveryInvalidGrantBodyIsIdentical(t *testing.T) {
	var bodies []string
	for range 3 {
		h := newHarness(t)
		h.fake.err = auth.ErrInvalidGrant
		resp := h.postForm(t, tokenPath, refreshBody("x"), map[string]string{"DPoP": "good-proof"})
		bodies = append(bodies, string(resp.Body))
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Fatalf("invalid_grant bodies differ: %q", bodies)
	}
}

func TestRevokeEndpoint(t *testing.T) {
	h := newHarness(t)
	resp := h.postForm(t, revokePath, url.Values{"token": {secretRefresh}, "token_type_hint": {"refresh_token"}}.Encode(), nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("revoke status = %d, headers = %v", resp.StatusCode, resp.Header)
	}
	if len(h.fake.revokes) != 1 || h.fake.revokes[0].Tenant != "shop" || h.fake.revokes[0].Token != secretRefresh {
		t.Fatalf("revokes = %+v", h.fake.revokes)
	}
	if missing := h.postForm(t, revokePath, "", nil); missing.StatusCode != http.StatusBadRequest || readError(t, missing).Error != "invalid_request" {
		t.Fatalf("revoke without token = %d", missing.StatusCode)
	}
	h.fake.err = auth.ErrUnsupportedTokenType
	if access := h.postForm(t, revokePath, url.Values{"token": {secretAccess}}.Encode(), nil); access.StatusCode != http.StatusBadRequest || readError(t, access).Error != "unsupported_token_type" {
		t.Fatalf("revoke with an access token = %d", access.StatusCode)
	}
	h.assertNoSecretsLogged(t)
}

func TestUnknownPathsAndMethods(t *testing.T) {
	h := newHarness(t)
	resp := h.do(t, http.MethodGet, tokenPath, "", nil)
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET token = %d, want 404 or 405", resp.StatusCode)
	}
	if other := h.postForm(t, "/nope", "", nil); other.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path = %d, want 404", other.StatusCode)
	}
}
