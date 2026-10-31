package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	profileSession = "s1aaaaaaaaaaaaaaaaaaaaaaaa"
	otherSession   = "s2aaaaaaaaaaaaaaaaaaaaaaaa"
)

func profileCheck(sessionID string, now time.Time) store.SessionCheck {
	return store.SessionCheck{TenantID: "shop", AccountID: lanID, SessionID: sessionID, Now: now}
}

func selfAudit() store.AuditEvent {
	return store.AuditEvent{TenantID: "shop", At: t0.Add(time.Minute), Actor: lanID, Action: "profile_update", AccountID: lanID, SessionID: profileSession}
}

func seedProfileAccount(t *testing.T, s fixture) {
	t.Helper()
	seedTenant(t, s, "shop")
	a := seedAccount(t, s, "shop", lanID, "lan")
	seedSessions(t, s, a, profileSession, otherSession)
}

func storedProfile(t *testing.T, s fixture) (name, avatar, by string) {
	t.Helper()
	if err := s.raw.QueryRow(context.Background(), "SELECT display_name, avatar_url, updated_by FROM accounts WHERE id = $1", lanID).Scan(&name, &avatar, &by); err != nil {
		t.Fatalf("read profile: %v", err)
	}
	return name, avatar, by
}

func TestUpdateMyProfileBumpsVerAndWritesOneAudit(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedProfileAccount(t, s)
	now := t0.Add(time.Minute)
	got, err := s.UpdateMyProfile(ctx, profileCheck(profileSession, now), store.ProfileUpdate{DisplayName: new("Lan Nguyen"), AvatarURL: new("https://cdn.example/lan.png")}, selfAudit())
	if err != nil {
		t.Fatalf("UpdateMyProfile: %v", err)
	}
	want := store.Profile{ID: lanID, Username: "lan", DisplayName: "Lan Nguyen", AvatarURL: "https://cdn.example/lan.png"}
	if got != want {
		t.Fatalf("profile = %+v; want %+v", got, want)
	}
	if name, avatar, by := storedProfile(t, s); name != want.DisplayName || avatar != want.AvatarURL || by != lanID {
		t.Fatalf("stored = %q %q %q", name, avatar, by)
	}
	wantVer(t, s, userKind, lanID, 2, "after update")
	wantAudits(t, s, "profile_update", 1)
	read, err := s.MyProfile(ctx, profileCheck(profileSession, now))
	if err != nil || read != want {
		t.Fatalf("MyProfile = %+v, %v; want %+v", read, err, want)
	}
	if _, err := s.UpdateMyProfile(ctx, profileCheck(profileSession, now), store.ProfileUpdate{AvatarURL: new("")}, selfAudit()); err != nil {
		t.Fatalf("clear avatar: %v", err)
	}
	if name, avatar, _ := storedProfile(t, s); name != "Lan Nguyen" || avatar != "" {
		t.Fatalf("after clear = %q %q; want the name kept", name, avatar)
	}
	wantVer(t, s, userKind, lanID, 3, "after clear")
	wantAudits(t, s, "profile_update", 2)
}

func TestUpdateMyProfileOnARevokedSessionChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedProfileAccount(t, s)
	if _, err := s.RevokeSession(ctx, profileSession, revocation(), audit("session_revoke")); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	now := t0.Add(time.Minute)
	update := store.ProfileUpdate{DisplayName: new("Someone Else")}
	cases := map[string]store.SessionCheck{
		"revoked":       profileCheck(profileSession, now),
		"idle expired":  profileCheck(otherSession, t0.Add(31*24*time.Hour)),
		"expired":       profileCheck(otherSession, t0.Add(91*24*time.Hour)),
		"other account": {TenantID: "shop", AccountID: minhID, SessionID: otherSession, Now: now},
		"other tenant":  {TenantID: "mall", AccountID: lanID, SessionID: otherSession, Now: now},
		"unknown":       profileCheck("s9aaaaaaaaaaaaaaaaaaaaaaaa", now),
	}
	for name, check := range cases {
		_, err := s.UpdateMyProfile(ctx, check, update, selfAudit())
		wantErr(t, err, apperr.ErrNotFound, name+" update")
		_, err = s.MyProfile(ctx, check)
		wantErr(t, err, apperr.ErrNotFound, name+" read")
	}
	if name, _, by := storedProfile(t, s); name != "lan" || by != "test" {
		t.Fatalf("stored = %q by %q; want unchanged", name, by)
	}
	wantVer(t, s, userKind, lanID, 1, "after refused updates")
	wantAudits(t, s, "profile_update", 0)
}
