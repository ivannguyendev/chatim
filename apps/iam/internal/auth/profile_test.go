package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	profileAccount = "a1aaaaaaaaaaaaaaaaaaaaaaaa"
	profileSession = "s1aaaaaaaaaaaaaaaaaaaaaaaa"
)

func (h *harness) seedProfile() auth.Caller {
	h.store.sessions[profileSession] = store.Session{
		ID: profileSession, TenantID: "shop", AccountID: profileAccount, State: store.StateActive,
		IdleExpiresAt: t0.Add(time.Hour), ExpiresAt: t0.Add(2 * time.Hour),
	}
	h.store.profiles[accountKey("shop", profileAccount)] = store.Profile{ID: profileAccount, Username: "lan", Email: "lan@example.com", DisplayName: "lan"}
	return auth.Caller{Tenant: "shop", Account: profileAccount, Session: profileSession, IP: "10.0.0.9"}
}

func TestMyProfileReadsThroughARunningSession(t *testing.T) {
	h := newHarness(t)
	caller := h.seedProfile()
	got, err := h.svc.MyProfile(context.Background(), caller)
	if err != nil {
		t.Fatalf("MyProfile: %v", err)
	}
	if got.Username != "lan" || got.Email != "lan@example.com" || got.DisplayName != "lan" {
		t.Fatalf("profile = %+v", got)
	}
	want := store.SessionCheck{TenantID: "shop", AccountID: profileAccount, SessionID: profileSession, Now: t0}
	if len(h.store.profile.checks) != 1 || h.store.profile.checks[0] != want {
		t.Fatalf("checks = %+v; want one %+v", h.store.profile.checks, want)
	}
}

func TestUpdateMyProfileRejectsBadInputBeforeTheStore(t *testing.T) {
	cases := map[string]struct {
		update store.ProfileUpdate
		want   error
	}{
		"nothing set":   {store.ProfileUpdate{}, apperr.ErrInvalidArgument},
		"blank name":    {store.ProfileUpdate{DisplayName: new("   ")}, directory.ErrDisplayName},
		"long name":     {store.ProfileUpdate{DisplayName: new(strings.Repeat("a", directory.MaxNameRunes+1))}, directory.ErrDisplayName},
		"http avatar":   {store.ProfileUpdate{AvatarURL: new("http://cdn.example/a.png")}, directory.ErrAvatarURL},
		"bad avatar":    {store.ProfileUpdate{DisplayName: new("Lan"), AvatarURL: new("https://user@cdn.example/a.png")}, directory.ErrAvatarURL},
		"invalid utf-8": {store.ProfileUpdate{DisplayName: new("\xff")}, directory.ErrDisplayName},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			caller := h.seedProfile()
			_, err := h.svc.UpdateMyProfile(context.Background(), caller, c.update)
			if !errors.Is(err, c.want) || !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("err = %v; want %v", err, c.want)
			}
			if len(h.store.profile.updates) != 0 {
				t.Fatal("invalid input reached the store")
			}
		})
	}
}

func TestUpdateMyProfileNormalizesAndAuditsAsTheAccount(t *testing.T) {
	h := newHarness(t)
	caller := h.seedProfile()
	got, err := h.svc.UpdateMyProfile(context.Background(), caller, store.ProfileUpdate{DisplayName: new("  Lan\u202e Nguyen \n"), AvatarURL: new("https://cdn.example/lan.png")})
	if err != nil {
		t.Fatalf("UpdateMyProfile: %v", err)
	}
	if got.DisplayName != "Lan Nguyen" || got.AvatarURL != "https://cdn.example/lan.png" {
		t.Fatalf("profile = %+v", got)
	}
	a := h.store.profile.audits[0]
	if a.Actor != profileAccount || a.AccountID != profileAccount || a.SessionID != profileSession || a.Action != "profile_update" || a.IP != "10.0.0.9" || a.TenantID != "shop" || !a.At.Equal(t0) {
		t.Fatalf("audit = %+v", a)
	}
	if a.Detail["fields"] != "display_name,avatar_url" {
		t.Fatalf("audit detail = %v", a.Detail)
	}
	cleared, err := h.svc.UpdateMyProfile(context.Background(), caller, store.ProfileUpdate{AvatarURL: new("")})
	if err != nil || cleared.AvatarURL != "" || cleared.DisplayName != "Lan Nguyen" {
		t.Fatalf("clear avatar = %+v, %v", cleared, err)
	}
	if h.store.profile.audits[1].Detail["fields"] != "avatar_url" {
		t.Fatalf("second audit detail = %v", h.store.profile.audits[1].Detail)
	}
}

func TestProfileSessionCheckErrorsPassThrough(t *testing.T) {
	h := newHarness(t)
	caller := h.seedProfile()
	revoked := h.store.sessions[profileSession]
	revoked.State = store.StateDisabled
	h.store.sessions[profileSession] = revoked
	if _, err := h.svc.MyProfile(context.Background(), caller); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("MyProfile on a revoked session = %v; want ErrSessionInvalid", err)
	}
	if _, err := h.svc.UpdateMyProfile(context.Background(), caller, store.ProfileUpdate{DisplayName: new("Lan")}); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("UpdateMyProfile on a revoked session = %v; want ErrSessionInvalid", err)
	}
	h.store.profile.err = apperr.ErrUnavailable
	if _, err := h.svc.MyProfile(context.Background(), caller); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("MyProfile on a store failure = %v; want unavailable", err)
	}
	if _, err := h.svc.UpdateMyProfile(context.Background(), caller, store.ProfileUpdate{DisplayName: new("Lan")}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("UpdateMyProfile on a store failure = %v; want unavailable", err)
	}
	if got := h.svc.Stats().StoreErrors.Load(); got != 2 {
		t.Fatalf("store errors = %d; want 2", got)
	}
}
