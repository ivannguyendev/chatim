package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1/iamv1connect"
)

func client(h *harness) iamv1connect.AuthServiceClient {
	return iamv1connect.NewAuthServiceClient(http.DefaultClient, h.server.URL)
}

func loginRequest(dpop string) *connect.Request[iamv1.LoginRequest] {
	req := connect.NewRequest(&iamv1.LoginRequest{Tenant: "shop", Username: "lan", Password: secretPassword, DeviceName: "iPhone"})
	if dpop != "" {
		req.Header().Set("DPoP", dpop)
	}
	req.Header().Set("X-Real-IP", "198.51.100.7")
	req.Header().Set("User-Agent", "chatim-test")
	return req
}

func TestLoginReturnsTokensWithNoStore(t *testing.T) {
	h := newHarness(t)
	resp, err := client(h).Login(context.Background(), loginRequest("good-proof"))
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if resp.Msg.GetTokenType() != "Bearer" || resp.Msg.GetExpiresIn() != 900 || resp.Msg.GetRefreshToken() != secretRefresh {
		t.Fatalf("response = %+v", resp.Msg)
	}
	if resp.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Login response headers = %v, want no-store", resp.Header())
	}
	got := h.fake.logins[0]
	if got.IP != "198.51.100.7" || got.UserAgent != "chatim-test" || got.Proof.Thumbprint != "jkt-/chatim.iam.v1.AuthService/Login" {
		t.Fatalf("login request = %+v", got)
	}
	h.assertNoSecretsLogged(t)
}

func TestLoginErrorCodes(t *testing.T) {
	cases := map[string]struct {
		dpop string
		err  error
		code connect.Code
		msg  string
	}{
		"missing dpop":        {"", nil, connect.CodeInvalidArgument, "invalid dpop proof"},
		"bad dpop":            {"bad-proof", nil, connect.CodeInvalidArgument, "invalid dpop proof"},
		"invalid credentials": {"good-proof", auth.ErrInvalidCredentials, connect.CodeUnauthenticated, "invalid credentials"},
		"must change":         {"good-proof", auth.ErrPasswordChangeRequired, connect.CodeFailedPrecondition, "password change required"},
		"too many":            {"good-proof", apperr.ErrResourceExhausted, connect.CodeResourceExhausted, "too many attempts, retry later"},
		"busy":                {"good-proof", apperr.ErrUnavailable, connect.CodeUnavailable, "temporarily unavailable"},
		"unexpected":          {"good-proof", errors.New("boom secret detail"), connect.CodeInternal, "request failed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.fake.err = c.err
			_, err := client(h).Login(context.Background(), loginRequest(c.dpop))
			if connect.CodeOf(err) != c.code {
				t.Fatalf("code = %v, want %v (%v)", connect.CodeOf(err), c.code, err)
			}
			var ce *connect.Error
			if !errors.As(err, &ce) || ce.Message() != c.msg {
				t.Fatalf("message = %v, want %q", err, c.msg)
			}
			h.assertNoSecretsLogged(t)
		})
	}
}

func TestSessionsRequireABearerToken(t *testing.T) {
	h := newHarness(t)
	c := client(h)
	req := connect.NewRequest(&iamv1.ListSessionsRequest{})
	if _, err := c.ListSessions(context.Background(), req); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("no bearer = %v, want unauthenticated", err)
	}
	bad := connect.NewRequest(&iamv1.ListSessionsRequest{})
	bad.Header().Set("Authorization", "Bearer forged")
	if _, err := c.ListSessions(context.Background(), bad); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("bad bearer = %v, want unauthenticated", err)
	}
	good := connect.NewRequest(&iamv1.ListSessionsRequest{})
	good.Header().Set("Authorization", "Bearer good-access")
	resp, err := c.ListSessions(context.Background(), good)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(resp.Msg.GetSessions()) != 1 || !resp.Msg.GetSessions()[0].GetCurrent() {
		t.Fatalf("sessions = %+v", resp.Msg.GetSessions())
	}
	revoke := connect.NewRequest(&iamv1.RevokeSessionRequest{SessionId: "s2"})
	revoke.Header().Set("Authorization", "Bearer good-access")
	if _, err := c.RevokeSession(context.Background(), revoke); err != nil || h.fake.revoked != "s2" {
		t.Fatalf("RevokeSession = %v, revoked %q", err, h.fake.revoked)
	}
	h.fake.err = apperr.ErrNotFound
	if _, err := c.RevokeSession(context.Background(), revoke); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("revoke unknown = %v, want not found", err)
	}
	h.assertNoSecretsLogged(t)
}

func TestChangePasswordNeedsNoTokenOrProof(t *testing.T) {
	h := newHarness(t)
	req := connect.NewRequest(&iamv1.ChangePasswordRequest{Tenant: "shop", Username: "lan", CurrentPassword: secretPassword, NewPassword: "a brand new passphrase"})
	if _, err := client(h).ChangePassword(context.Background(), req); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	h.fake.err = fmt.Errorf("%w: password must be 12 to 128 characters", apperr.ErrInvalidArgument)
	_, err := client(h).ChangePassword(context.Background(), req)
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeInvalidArgument || ce.Message() != "password must be 12 to 128 characters" {
		t.Fatalf("weak password = %v, want the policy message", err)
	}
	h.assertNoSecretsLogged(t)
}

func TestLoginRejectsTwoProofsAndOversizedBodies(t *testing.T) {
	h := newHarness(t)
	req := loginRequest("good-proof")
	req.Header().Add("DPoP", "good-proof")
	if _, err := client(h).Login(context.Background(), req); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("two proofs = %v, want invalid argument", err)
	}
	big := loginRequest("good-proof")
	big.Msg.Password = strings.Repeat("p", 20<<10)
	_, err := client(h).Login(context.Background(), big)
	if code := connect.CodeOf(err); code != connect.CodeResourceExhausted && code != connect.CodeInvalidArgument {
		t.Fatalf("oversized login = %v, want resource exhausted or invalid argument", err)
	}
	if len(h.fake.logins) != 0 {
		t.Fatal("login reached the service with two proofs or an oversized body")
	}
}

func TestBearerParsing(t *testing.T) {
	h := newHarness(t)
	for name, values := range map[string][]string{
		"lowercase scheme": {"bearer good-access"},
		"two headers":      {"Bearer good-access", "Bearer good-access"},
		"double space":     {"Bearer  good-access"},
	} {
		req := connect.NewRequest(&iamv1.ListSessionsRequest{})
		for _, v := range values {
			req.Header().Add("Authorization", v)
		}
		_, err := client(h).ListSessions(context.Background(), req)
		wantOK := name == "lowercase scheme"
		if (err == nil) != wantOK {
			t.Fatalf("%s: err = %v, want ok %v", name, err, wantOK)
		}
	}
}
