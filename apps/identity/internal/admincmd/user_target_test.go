package admincmd_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	targetIssuer = "https://shop.example"
	targetSub    = "0901000000"
	targetUserID = "a1aaaaaaaaaaaaaaaaaaaaaaaa"
)

type userTargetStore struct {
	closedStore
	resolved []string
	writes   []string
}

func (s *userTargetStore) ResolveUser(_ context.Context, issuer, sub string) (string, string, error) {
	s.resolved = append(s.resolved, issuer+" "+sub)
	if issuer != targetIssuer || sub != targetSub {
		return "", "", apperr.ErrNotFound
	}
	return "shop", targetUserID, nil
}

func (s *userTargetStore) SetUserState(_ context.Context, tenantID, id string, state store.State, _ time.Time, _ string) error {
	s.writes = append(s.writes, "state "+tenantID+" "+id+" "+map[store.State]string{store.StateActive: "active", store.StateDisabled: "disabled"}[state])
	return nil
}

func (s *userTargetStore) SetProfile(_ context.Context, tenantID, id string, u store.ProfileUpdate, _ time.Time, _ string) error {
	s.writes = append(s.writes, "profile "+tenantID+" "+id+" "+*u.DisplayName)
	return nil
}

func (s *userTargetStore) SetUserGroups(_ context.Context, tenantID, id string, groups []string, _ time.Time, _ string) error {
	s.writes = append(s.writes, "groups "+tenantID+" "+id+" "+strings.Join(groups, ","))
	return nil
}

func userTargetEnv(t *testing.T) (*userTargetStore, admincmd.Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	st := &userTargetStore{}
	env, out, logs := testEnv(t, func(context.Context) (admincmd.Store, error) { return st, nil })
	return st, env, out, logs
}

func TestUserCommandsTakeExactlyOneUserPair(t *testing.T) {
	st, env, out, _ := userTargetEnv(t)
	cases := [][]string{
		{"user-disable"},
		{"user-disable", "-tenant", "shop", "-user", targetUserID, "-issuer", targetIssuer, "-sub", targetSub},
		{"user-disable", "-tenant", "shop", "-sub", targetSub},
		{"user-disable", "-issuer", targetIssuer},
		{"user-enable", "-sub", targetSub},
		{"user-enable", "-issuer", targetIssuer, "-sub", ""},
		{"user-enable", "-issuer", targetIssuer, "-sub", "09 01"},
		{"user-enable", "-issuer", targetIssuer, "-sub", strings.Repeat("a", 256)},
		{"user-enable", "-issuer", "https://shop example", "-sub", targetSub},
		{"user-set", "-issuer", targetIssuer, "-sub", targetSub},
		{"user-set", "-name", "Lan"},
		{"user-groups", "-issuer", targetIssuer, "-sub", targetSub},
		{"user-groups", "-tenant", "shop", "-user", targetUserID, "-issuer", targetIssuer, "-sub", targetSub, "-groups", ""},
		{"user-find"},
		{"user-find", "-issuer", targetIssuer},
		{"user-find", "-sub", targetSub},
		{"user-find", "-issuer", targetIssuer, "-sub", "09\x0101"},
		{"user-find", "-tenant", "shop", "-user", targetUserID},
		{"user-find", "-issuer", targetIssuer, "-sub", targetSub, "extra"},
	}
	for _, args := range cases {
		if code := admincmd.Run(context.Background(), env, args); code != 2 {
			t.Fatalf("Run(%q) = %d, want 2", args, code)
		}
	}
	if len(st.resolved)+len(st.writes) != 0 || out.Len() != 0 {
		t.Fatalf("resolved %v, wrote %v, out %q on usage errors; want nothing", st.resolved, st.writes, out)
	}
}

func TestUserCommandsActOnTheUserOfTheIssuerSubject(t *testing.T) {
	st, env, out, _ := userTargetEnv(t)
	bySub := []string{"-issuer", targetIssuer, "-sub", targetSub}
	for _, args := range [][]string{
		append([]string{"user-disable"}, bySub...),
		append([]string{"user-enable"}, bySub...),
		append([]string{"user-set", "-name", "Lan"}, bySub...),
		append([]string{"user-groups", "-groups", "sales"}, bySub...),
		{"user-disable", "-tenant", "shop", "-user", "a2aaaaaaaaaaaaaaaaaaaaaaaa"},
	} {
		if code := admincmd.Run(context.Background(), env, args); code != 0 {
			t.Fatalf("Run(%q) = %d, want 0", args, code)
		}
	}
	want := []string{
		"state shop " + targetUserID + " disabled",
		"state shop " + targetUserID + " active",
		"profile shop " + targetUserID + " Lan",
		"groups shop " + targetUserID + " sales",
		"state shop a2aaaaaaaaaaaaaaaaaaaaaaaa disabled",
	}
	if !slices.Equal(st.writes, want) || len(st.resolved) != 4 {
		t.Fatalf("writes %v after %d resolves; want %v after 4", st.writes, len(st.resolved), want)
	}
	if !strings.HasPrefix(out.String(), "user "+targetUserID+": user_disable\nuser "+targetUserID+": user_enable\n") {
		t.Fatalf("out = %q; want the resolved user id", out)
	}
}

func TestUserFindPrintsTheTenantAndUserID(t *testing.T) {
	_, env, out, _ := userTargetEnv(t)
	if code := admincmd.Run(context.Background(), env, []string{"user-find", "-issuer", targetIssuer, "-sub", targetSub}); code != 0 {
		t.Fatalf("Run = %d, want 0", code)
	}
	if out.String() != "shop "+targetUserID+"\n" {
		t.Fatalf("out = %q; want tenant and user id", out)
	}
}

func TestUserCommandsOfAnUnknownIssuerSubjectExitWithOne(t *testing.T) {
	st, env, out, logs := userTargetEnv(t)
	for _, args := range [][]string{
		{"user-find", "-issuer", "https://ghost.example", "-sub", targetSub},
		{"user-find", "-issuer", targetIssuer, "-sub", "0909999999"},
		{"user-disable", "-issuer", targetIssuer, "-sub", "0909999999"},
		{"user-set", "-issuer", targetIssuer, "-sub", "0909999999", "-name", "Lan"},
		{"user-groups", "-issuer", targetIssuer, "-sub", "0909999999", "-groups", ""},
	} {
		if code := admincmd.Run(context.Background(), env, args); code != 1 {
			t.Fatalf("Run(%q) = %d, want 1", args, code)
		}
	}
	if len(st.writes) != 0 || out.Len() != 0 || !strings.Contains(logs.String(), "not found") || strings.Contains(logs.String(), "0909999999") {
		t.Fatalf("wrote %v, out %q, logs %s; want nothing written and not found logged without the subject", st.writes, out, logs)
	}
}

func TestIssuerAddPassesTheRealm(t *testing.T) {
	st := newTrustStore()
	env, out := trustEnv(t, st)
	if code := admincmd.Run(context.Background(), env, []string{"issuer-add", "-issuer", "https://idp.shop.example", "-tenant", "shop", "-realm", targetIssuer}); code != 0 {
		t.Fatalf("Run = %d, want 0", code)
	}
	if len(st.realms) != 1 || st.realms[0] != targetIssuer {
		t.Fatalf("realms %q; want %s", st.realms, targetIssuer)
	}
	if out.String() != "issuer https://idp.shop.example trusted for tenant shop, realm "+targetIssuer+"\n" {
		t.Fatalf("out = %q", out)
	}
	if code := admincmd.Run(context.Background(), env, []string{"issuer-add", "-issuer", "https://x.example", "-tenant", "shop", "-realm", "bad realm"}); code != 2 {
		t.Fatalf("Run with a bad realm = %d, want 2", code)
	}
	if len(st.created) != 1 {
		t.Fatalf("created %v; want no write for a bad realm", st.created)
	}
}
