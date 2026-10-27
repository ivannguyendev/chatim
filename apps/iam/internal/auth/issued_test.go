package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func issuedAt(t *testing.T, token string) time.Time {
	t.Helper()
	parsed, _, err := jwt.NewParser().ParseUnverified(token, &jwt.RegisteredClaims{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return parsed.Claims.(*jwt.RegisteredClaims).IssuedAt.Time
}

func TestTokensCarryTheStoreIssueTime(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan", lanPassword, false)
	h.store.issued = t0.Add(-3 * time.Second)
	first, err := h.login("lan", lanPassword, "jkt-lan")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := issuedAt(t, first.AccessToken); !got.Equal(h.store.issued) || !first.ExpiresAt.Equal(h.store.issued.Add(15*time.Minute)) {
		t.Fatalf("login iat %v exp %v; want the store time %v", got, first.ExpiresAt, h.store.issued)
	}
	h.now = t0.Add(time.Minute)
	h.store.issued = t0.Add(50 * time.Second)
	second, err := h.refresh(first.RefreshToken, "jkt-lan", "j1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := issuedAt(t, second.AccessToken); !got.Equal(h.store.issued) {
		t.Fatalf("refresh iat %v; want the store time %v", got, h.store.issued)
	}
}
