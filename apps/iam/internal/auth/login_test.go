package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/guard"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const lanPassword = "correct horse battery"

func TestLoginCreatesASessionBoundToTheDeviceKey(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan", lanPassword, false)
	tok, err := h.login("Lan", lanPassword, "jkt-lan")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" || sidOf(tok.RefreshToken) != tok.SessionID {
		t.Fatalf("tokens = %+v; want access, refresh starting with the session id", tok)
	}
	s := h.store.sessions[tok.SessionID]
	if s.KeyThumbprint != "jkt-lan" || s.State != store.StateActive || !s.ExpiresAt.Equal(t0.Add(90*24*time.Hour)) {
		t.Fatalf("session = %+v; want bound to jkt-lan, active, 90-day limit", s)
	}
	if !tok.ExpiresAt.Equal(t0.Add(15 * time.Minute)) {
		t.Fatalf("access expiry = %v, want 15 minutes", tok.ExpiresAt)
	}
}

func TestEveryFailedLoginHashesOnceAndLooksTheSame(t *testing.T) {
	cases := map[string]func(h *harness){
		"wrong password":   func(h *harness) {},
		"unknown username": func(h *harness) {},
		"disabled account": func(h *harness) {
			a := h.store.accounts[accountKey("shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa")]
			a.State = store.StateDisabled
			h.store.accounts[accountKey("shop", a.ID)] = a
		},
		"disabled tenant": func(h *harness) { h.store.tenants["shop"] = store.Tenant{ID: "shop", State: store.StateDisabled} },
		"throttled account with the right password": func(h *harness) {
			a := h.store.accounts[accountKey("shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa")]
			a.NextLoginAt = t0.Add(30 * time.Second)
			h.store.accounts[accountKey("shop", a.ID)] = a
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.addAccount(t, "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan", lanPassword, false)
			setup(h)
			username, pw := "lan", "wrong password value"
			switch name {
			case "unknown username":
				username = "nobody"
			case "disabled account", "disabled tenant", "throttled account with the right password":
				pw = lanPassword
			}
			_, err := h.login(username, pw, "jkt-lan")
			if !errors.Is(err, auth.ErrInvalidCredentials) || !errors.Is(err, apperr.ErrUnauthenticated) {
				t.Fatalf("Login = %v, want ErrInvalidCredentials", err)
			}
			if h.hasher.Hashes() != 1 {
				t.Fatalf("hashes = %d, want exactly 1", h.hasher.Hashes())
			}
			if len(h.store.sessions) != 0 {
				t.Fatal("a failed login created a session")
			}
		})
	}
}

func TestWrongPasswordsBuildUpTheBackoff(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan", lanPassword, false)
	for range 6 {
		_, _ = h.login("lan", "wrong password value", "jkt-lan")
	}
	a := h.store.accounts[accountKey("shop", "a1aaaaaaaaaaaaaaaaaaaaaaaa")]
	if a.FailedLoginCount != 6 || !a.NextLoginAt.Equal(t0.Add(time.Second)) {
		t.Fatalf("failed = %d, next = %v; want 6 and t0+1s", a.FailedLoginCount, a.NextLoginAt)
	}
	if _, err := h.login("lan", lanPassword, "jkt-lan"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("right password while throttled = %v, want ErrInvalidCredentials", err)
	}
	h.now = t0.Add(2 * time.Second)
	if _, err := h.login("lan", lanPassword, "jkt-lan"); err != nil {
		t.Fatalf("login after the wait: %v", err)
	}
}

func TestMustChangeIsOnlyRevealedToTheRightPassword(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan", lanPassword, true)
	if _, err := h.login("lan", "wrong password value", "jkt-lan"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("wrong password = %v, want ErrInvalidCredentials", err)
	}
	if _, err := h.login("lan", lanPassword, "jkt-lan"); !errors.Is(err, auth.ErrPasswordChangeRequired) || !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("right password = %v, want ErrPasswordChangeRequired", err)
	}
	if len(h.store.sessions) != 0 {
		t.Fatal("a must-change login created a session")
	}
}

func TestLoginLimitsPerIPAndHashSlots(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan", lanPassword, false)
	limited := auth.New(auth.Deps{
		Store: h.store, Hasher: h.hasher, IPs: guard.NewIPLimiter(1, 1, 10), Slots: guard.NewHashSlots(4),
		Signers: staticSigners{}, Now: func() time.Time { return t0 },
	}, auth.Config{})
	req := auth.LoginRequest{Tenant: "shop", Username: "lan", Password: "wrong password value", IP: "10.0.0.9"}
	_, _ = limited.Login(context.Background(), req)
	if _, err := limited.Login(context.Background(), req); !errors.Is(err, apperr.ErrResourceExhausted) {
		t.Fatalf("second login from one ip = %v, want ErrResourceExhausted", err)
	}
	busy := auth.New(auth.Deps{
		Store: h.store, Hasher: h.hasher, IPs: guard.NewIPLimiter(100, 100, 10), Slots: guard.NewHashSlots(0),
		Signers: staticSigners{}, Now: func() time.Time { return t0 },
	}, auth.Config{})
	if _, err := busy.Login(context.Background(), req); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("login with no hash slot = %v, want ErrUnavailable", err)
	}
}

func TestLoginRejectsMalformedInputWithoutHashing(t *testing.T) {
	h := newHarness(t)
	for _, username := range []string{"", "has space", "UPPERé", string(make([]byte, 65))} {
		if _, err := h.login(username, lanPassword, "jkt-lan"); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("Login(%q) = %v, want ErrInvalidCredentials", username, err)
		}
	}
	if _, err := h.svc.Login(context.Background(), auth.LoginRequest{Tenant: "Bad Tenant", Username: "lan", Password: lanPassword, Proof: auth.Proof{Thumbprint: "j", JTI: "j"}}); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("bad tenant = %v, want ErrInvalidCredentials", err)
	}
	if h.hasher.Hashes() != 0 {
		t.Fatalf("hashes = %d, want 0 for malformed input", h.hasher.Hashes())
	}
}

func TestChangePasswordRevokesEverySession(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan", lanPassword, true)
	ctx := context.Background()
	req := auth.ChangePasswordRequest{Tenant: "shop", Username: "lan", Current: lanPassword, New: "a brand new passphrase", IP: "10.0.0.1"}
	if err := h.svc.ChangePassword(ctx, req); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	tok, err := h.login("lan", "a brand new passphrase", "jkt-lan")
	if err != nil {
		t.Fatalf("login with the new password: %v", err)
	}
	again := auth.ChangePasswordRequest{Tenant: "shop", Username: "lan", Current: "a brand new passphrase", New: "yet another passphrase", IP: "10.0.0.1"}
	if err := h.svc.ChangePassword(ctx, again); err != nil {
		t.Fatalf("second ChangePassword: %v", err)
	}
	if h.store.sessions[tok.SessionID].State != store.StateDisabled {
		t.Fatal("password change left an old session live")
	}
}

func TestChangePasswordValidatesTheNewPassword(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "a1aaaaaaaaaaaaaaaaaaaaaaaa", "lan", lanPassword, false)
	ctx := context.Background()
	for name, next := range map[string]string{"too short": "short", "unchanged": lanPassword} {
		err := h.svc.ChangePassword(ctx, auth.ChangePasswordRequest{Tenant: "shop", Username: "lan", Current: lanPassword, New: next, IP: "10.0.0.1"})
		if !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: ChangePassword = %v, want ErrInvalidArgument", name, err)
		}
	}
	err := h.svc.ChangePassword(ctx, auth.ChangePasswordRequest{Tenant: "shop", Username: "lan", Current: "wrong password value", New: "a brand new passphrase", IP: "10.0.0.1"})
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("wrong current password = %v, want ErrInvalidCredentials", err)
	}
}
