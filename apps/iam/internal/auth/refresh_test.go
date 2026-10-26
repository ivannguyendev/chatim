package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func loggedIn(t *testing.T) (*harness, auth.Tokens) {
	t.Helper()
	h := newHarness(t)
	h.addAccount(t, "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan", lanPassword, false)
	tok, err := h.login("lan", lanPassword, "jkt-lan")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return h, tok
}

func TestRefreshRotatesAndRetriesInsideGrace(t *testing.T) {
	h, first := loggedIn(t)
	h.now = t0.Add(12 * time.Minute)
	second, err := h.refresh(first.RefreshToken, "jkt-lan", "j1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken || second.SessionID != first.SessionID {
		t.Fatalf("refresh did not rotate within the session")
	}
	h.now = h.now.Add(5 * time.Second)
	third, err := h.refresh(first.RefreshToken, "jkt-lan", "j2")
	if err != nil {
		t.Fatalf("first retry: %v", err)
	}
	h.now = h.now.Add(5 * time.Second)
	if _, err := h.refresh(first.RefreshToken, "jkt-lan", "j3"); err != nil {
		t.Fatalf("second retry: %v", err)
	}
	if third.RefreshToken == second.RefreshToken {
		t.Fatal("retry returned an already issued token")
	}
}

func TestEveryInvalidRefreshReturnsTheSameError(t *testing.T) {
	h, tok := loggedIn(t)
	garbage := tok.SessionID + "." + strings.Repeat("A", 43)
	cases := map[string]struct{ token, jkt, tenant string }{
		"other device key":  {tok.RefreshToken, "jkt-thief", "shop"},
		"garbage secret":    {garbage, "jkt-lan", "shop"},
		"malformed":         {"nope", "jkt-lan", "shop"},
		"unknown session":   {"bbbbbbbbbbbbbbbbbbbbbbbbbb." + strings.Repeat("A", 43), "jkt-lan", "shop"},
		"other tenant path": {tok.RefreshToken, "jkt-lan", "blog"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := h.svc.Refresh(context.Background(), auth.RefreshRequest{Tenant: c.tenant, RefreshToken: c.token, Proof: auth.Proof{Thumbprint: c.jkt, JTI: "j-" + name}})
			if !errors.Is(err, auth.ErrInvalidGrant) || err.Error() != auth.ErrInvalidGrant.Error() {
				t.Fatalf("Refresh = %v, want exactly ErrInvalidGrant", err)
			}
		})
	}
	if h.store.sessions[tok.SessionID].State != store.StateActive {
		t.Fatal("an invalid refresh revoked the session")
	}
}

func TestReusingAnOldTokenRevokesTheSession(t *testing.T) {
	h, first := loggedIn(t)
	if _, err := h.refresh(first.RefreshToken, "jkt-lan", "j1"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	h.now = t0.Add(31 * time.Second)
	if _, err := h.refresh(first.RefreshToken, "jkt-lan", "j2"); !errors.Is(err, auth.ErrInvalidGrant) {
		t.Fatalf("reuse = %v, want ErrInvalidGrant", err)
	}
	if s := h.store.sessions[first.SessionID]; s.State != store.StateDisabled || s.RevokedReason != "refresh_reuse" {
		t.Fatalf("session = %+v; want revoked for refresh_reuse", s)
	}
}

func TestReplayedProofIsRejected(t *testing.T) {
	h, first := loggedIn(t)
	second, err := h.refresh(first.RefreshToken, "jkt-lan", "j1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, err := h.refresh(second.RefreshToken, "jkt-lan", "j1"); !errors.Is(err, auth.ErrProofReplayed) {
		t.Fatalf("replayed jti = %v, want ErrProofReplayed", err)
	}
}

func TestRefreshFailsForADisabledAccount(t *testing.T) {
	h, tok := loggedIn(t)
	a := h.store.accounts[accountKey("shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa")]
	a.State = store.StateDisabled
	h.store.accounts[accountKey("shop", a.ID)] = a
	if _, err := h.refresh(tok.RefreshToken, "jkt-lan", "j1"); !errors.Is(err, auth.ErrInvalidGrant) {
		t.Fatalf("refresh of a disabled account = %v, want ErrInvalidGrant", err)
	}
}

func TestRevokeOnlyActsOnARealToken(t *testing.T) {
	h, tok := loggedIn(t)
	ctx := context.Background()
	garbage := tok.SessionID + "." + strings.Repeat("A", 43)
	for _, token := range []string{garbage, "nope", "bbbbbbbbbbbbbbbbbbbbbbbbbb." + strings.Repeat("A", 43)} {
		if err := h.svc.Revoke(ctx, auth.RevokeRequest{Tenant: "shop", Token: token}); err != nil {
			t.Fatalf("Revoke(%q) = %v, want nil", token, err)
		}
	}
	if err := h.svc.Revoke(ctx, auth.RevokeRequest{Tenant: "blog", Token: tok.RefreshToken}); err != nil {
		t.Fatalf("Revoke other tenant = %v", err)
	}
	if h.store.sessions[tok.SessionID].State != store.StateActive {
		t.Fatal("revoke with a wrong secret or tenant ended the session")
	}
	if err := h.svc.Revoke(ctx, auth.RevokeRequest{Tenant: "shop", Token: tok.AccessToken}); !errors.Is(err, auth.ErrUnsupportedTokenType) {
		t.Fatalf("Revoke(access token) = %v, want ErrUnsupportedTokenType", err)
	}
	if err := h.svc.Revoke(ctx, auth.RevokeRequest{Tenant: "shop", Token: tok.RefreshToken}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if s := h.store.sessions[tok.SessionID]; s.State != store.StateDisabled || s.RevokedReason != "logout" {
		t.Fatalf("session = %+v; want revoked by logout", s)
	}
}

func TestExpiredAndRevokedSessionsReturnTheSameError(t *testing.T) {
	h, tok := loggedIn(t)
	h.now = t0.Add(100 * 24 * time.Hour)
	if _, err := h.refresh(tok.RefreshToken, "jkt-lan", "j1"); err == nil || err.Error() != auth.ErrInvalidGrant.Error() {
		t.Fatalf("expired = %v, want exactly ErrInvalidGrant", err)
	}
	h.now = t0
	_ = h.svc.Revoke(context.Background(), auth.RevokeRequest{Tenant: "shop", Token: tok.RefreshToken})
	if _, err := h.refresh(tok.RefreshToken, "jkt-lan", "j2"); err == nil || err.Error() != auth.ErrInvalidGrant.Error() {
		t.Fatalf("revoked = %v, want exactly ErrInvalidGrant", err)
	}
}

func TestStoreFailuresDuringRefreshAreNotReportedAsDeadSessions(t *testing.T) {
	h, tok := loggedIn(t)
	h.store.failRead = apperr.ErrUnavailable
	if _, err := h.refresh(tok.RefreshToken, "jkt-lan", "j1"); !errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, auth.ErrInvalidGrant) {
		t.Fatalf("refresh during a store outage = %v, want ErrUnavailable", err)
	}
	if h.store.sessions[tok.SessionID].State != store.StateActive {
		t.Fatal("a store outage ended the session")
	}
}

func TestReuseRevokeFailureIsSurfaced(t *testing.T) {
	h, first := loggedIn(t)
	if _, err := h.refresh(first.RefreshToken, "jkt-lan", "j1"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	h.now = t0.Add(time.Minute)
	h.store.failRev = apperr.ErrUnavailable
	if _, err := h.refresh(first.RefreshToken, "jkt-lan", "j2"); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("reuse with a failing revoke = %v, want ErrUnavailable", err)
	}
}
