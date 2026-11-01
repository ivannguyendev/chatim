package api_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
	"github.com/ivannguyendev/chatim/pkg/revocation"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

func TestNewRefusesAdminWithoutItsDependencies(t *testing.T) {
	gate, _ := userlimit.NewGate(1)
	users, _ := userlimit.NewUsers(userlimit.UserConfig{Rate: 1, Burst: 1, Inflight: 1, Capacity: 1})
	base := func() api.Options {
		return api.Options{
			Auth: &fakeAuth{}, Proofs: fakeProofs{}, Callers: fakeCallers{}, Keys: fakeKeys{}, BodyMax: 1024,
			Admin: &fakeAdmin{}, Claims: testClaims, Revocations: newTestRevocations(t), AdminGate: gate, AdminUsers: users,
		}
	}
	if _, err := api.New(base()); err != nil {
		t.Fatalf("full admin options: %v", err)
	}
	for name, drop := range map[string]func(*api.Options){
		"claims":      func(o *api.Options) { o.Claims = nil },
		"revocations": func(o *api.Options) { o.Revocations = nil },
		"gate":        func(o *api.Options) { o.AdminGate = nil },
		"users":       func(o *api.Options) { o.AdminUsers = nil },
	} {
		o := base()
		drop(&o)
		if _, err := api.New(o); err == nil {
			t.Fatalf("admin without %s accepted", name)
		}
	}
}

func TestAdminNeedsABearerToken(t *testing.T) {
	a := newAdminHarness(t)
	_, err := a.client.ListRoles(context.Background(), connect.NewRequest(&iamv1.ListRolesRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated, "no authorization header")
	for name, call := range adminCalls {
		wantCode(t, call(a, ""), connect.CodeUnauthenticated, name+" empty bearer")
		wantCode(t, call(a, "forged"), connect.CodeUnauthenticated, name+" forged")
	}
	if n := len(a.admin.recorded()); n != 0 {
		t.Fatalf("admin calls = %d; want 0", n)
	}
	if a.gate.InFlight() != 0 {
		t.Fatalf("gate in flight = %d after refusals", a.gate.InFlight())
	}
	if got := a.stats.Unauthenticated.Load(); got != uint64(1+2*len(adminCalls)) {
		t.Fatalf("unauthenticated = %d; want %d", got, 1+2*len(adminCalls))
	}
}

func TestAdminRefusesARevokedTokenFromMemory(t *testing.T) {
	list := newTestRevocations(t)
	a := newAdminHarness(t, func(o *api.Options) { o.Revocations = list })
	for name, call := range adminCalls {
		ce := wantCode(t, call(a, tokenRevoked), connect.CodeUnauthenticated, name)
		if ce.message != "session revoked" {
			t.Fatalf("%s: message = %q", name, ce.message)
		}
	}
	if err := adminCalls["ListRoles"](a, tokenOld); err != nil {
		t.Fatalf("before the tenant revocation: %v", err)
	}
	list.Merge(revocation.Snapshot{Tenants: []revocation.Tenant{{ID: "shop", RevokedBefore: t0, ExpiresAt: t0.Add(15 * time.Minute)}}}, t0)
	wantCode(t, adminCalls["ListRoles"](a, tokenOld), connect.CodeUnauthenticated, "issued before the tenant revocation")
	if err := adminCalls["ListRoles"](a, tokenLan); err != nil {
		t.Fatalf("issued after the tenant revocation: %v", err)
	}
	if n := len(a.admin.recorded()); n != 2 {
		t.Fatalf("admin calls = %d; want 2, revoked tokens never reach it", n)
	}
}

func TestAdminLimitsPerUserAndPerNode(t *testing.T) {
	users, err := userlimit.NewUsers(userlimit.UserConfig{Rate: 1, Burst: 1, Inflight: 4, Capacity: 10, Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := userlimit.NewGate(1)
	if err != nil {
		t.Fatal(err)
	}
	a := newAdminHarness(t, func(o *api.Options) { o.AdminUsers, o.AdminGate = users, gate })
	if err := adminCalls["GetRole"](a, tokenLan); err != nil {
		t.Fatalf("first call: %v", err)
	}
	wantCode(t, adminCalls["GetRole"](a, tokenLan), connect.CodeResourceExhausted, "over the user rate")
	if err := adminCalls["GetRole"](a, tokenMall); err != nil {
		t.Fatalf("another user: %v", err)
	}
	gate.Enter()
	ce := wantCode(t, adminCalls["ListGroups"](a, tokenMall), connect.CodeUnavailable, "node full")
	gate.Leave()
	if ce.retryAfter != "1" {
		t.Fatalf("Retry-After = %q; want 1", ce.retryAfter)
	}
	if gate.InFlight() != 0 {
		t.Fatalf("gate in flight = %d; want 0", gate.InFlight())
	}
	if a.stats.UserLimited.Load() != 1 || a.stats.InflightRejected.Load() != 1 || a.stats.Limited.Load() != 1 || a.stats.Unavailable.Load() != 1 {
		t.Fatalf("user limited %d inflight rejected %d limited %d unavailable %d; want 1 each",
			a.stats.UserLimited.Load(), a.stats.InflightRejected.Load(), a.stats.Limited.Load(), a.stats.Unavailable.Load())
	}
	if n := len(a.admin.recorded()); n != 2 {
		t.Fatalf("admin calls = %d; want 2", n)
	}
}

func TestAdminErrorCodes(t *testing.T) {
	_, longPermission := authz.Parse("iam:" + strings.Repeat("é", 300))
	cases := []struct {
		err        error
		code       connect.Code
		message    string
		retryAfter string
	}{
		{fmt.Errorf("%w: missing iam:roles.write", apperr.ErrPermissionDenied), connect.CodePermissionDenied, "missing iam:roles.write", ""},
		{authz.ErrRoleGrantsMore, connect.CodePermissionDenied, "role grants permissions the caller lacks", ""},
		{apperr.ErrPermissionDenied, connect.CodePermissionDenied, "permission denied", ""},
		{fmt.Errorf("%w: builtin role cannot change", apperr.ErrFailedPrecondition), connect.CodeFailedPrecondition, "builtin role cannot change", ""},
		{auth.ErrPasswordChangeRequired, connect.CodeFailedPrecondition, "password change required", ""},
		{authz.ErrRoleID, connect.CodeInvalidArgument, strings.TrimPrefix(authz.ErrRoleID.Error(), "invalid argument: "), ""},
		{fmt.Errorf("%w: role_id", apperr.ErrNotFound), connect.CodeNotFound, "not found", ""},
		{fmt.Errorf("%w: roles_pkey", apperr.ErrAlreadyExists), connect.CodeAlreadyExists, "already exists", ""},
		{auth.ErrSessionInvalid, connect.CodeUnauthenticated, "session is not active", ""},
		{fmt.Errorf("%w: 40001", apperr.ErrUnavailable), connect.CodeUnavailable, "temporarily unavailable", "1"},
	}
	for _, tc := range cases {
		a := newAdminHarness(t)
		a.admin.setErr("UpdateRole", tc.err)
		ce := wantCode(t, adminCalls["UpdateRole"](a, tokenLan), tc.code, tc.err.Error())
		if ce.message != tc.message || ce.retryAfter != tc.retryAfter {
			t.Fatalf("%v: message %q retry %q; want %q, %q", tc.err, ce.message, ce.retryAfter, tc.message, tc.retryAfter)
		}
	}
	a := newAdminHarness(t)
	a.admin.setErr("CreateRole", longPermission)
	ce := wantCode(t, adminCalls["CreateRole"](a, tokenLan), connect.CodeInvalidArgument, "long echoed input")
	if len(ce.message) > 200 || !utf8.ValidString(ce.message) || !strings.HasPrefix(ce.message, "permission ") {
		t.Fatalf("message of %d bytes not clipped to a valid 200 bytes: %q", len(ce.message), ce.message)
	}
}

func TestEveryAdminProcedureHasARequiredPermission(t *testing.T) {
	methods := iamv1.File_chatim_iam_v1_admin_proto.Services().ByName("AdminService").Methods()
	var names []string
	for i := range methods.Len() {
		name := string(methods.Get(i).Name())
		names = append(names, name)
		if _, ok := authz.Required[name]; !ok {
			t.Errorf("AdminService/%s has no required permission", name)
		}
		if _, ok := adminCalls[name]; !ok {
			t.Errorf("AdminService/%s is missing from the test calls", name)
		}
	}
	slices.Sort(names)
	if want := slices.Sorted(maps.Keys(authz.Required)); !slices.Equal(names, want) {
		t.Fatalf("procedures %v; authz.Required %v", names, want)
	}
	if len(adminCalls) != len(names) {
		t.Fatalf("test calls = %d; procedures = %d", len(adminCalls), len(names))
	}
}

func TestAdminStatsCountDenialsPerRPC(t *testing.T) {
	a := newAdminHarness(t)
	denied := fmt.Errorf("%w: missing iam:roles.write", apperr.ErrPermissionDenied)
	a.admin.setErr("CreateRole", denied)
	a.admin.setErr("ListGroups", denied)
	a.admin.setErr("GetRole", apperr.ErrNotFound)
	a.admin.setErr("CreateGroup", fmt.Errorf("%w: groups_pkey", apperr.ErrAlreadyExists))
	a.admin.setErr("DeleteGroup", fmt.Errorf("%w: group has live child groups", apperr.ErrFailedPrecondition))
	a.admin.setErr("DeleteRole", authz.ErrRoleID)
	a.admin.setErr("GetAccount", errors.New("boom"))
	for _, rpc := range []string{"CreateRole", "CreateRole", "ListGroups", "GetRole", "CreateGroup", "DeleteGroup", "DeleteRole", "GetAccount", "ListRoles"} {
		_ = adminCalls[rpc](a, tokenLan)
	}
	_ = adminCalls["ListRoles"](a, "forged")
	got := a.stats.DeniedByRPC()
	if want := slices.Sorted(maps.Keys(authz.Required)); !slices.Equal(slices.Sorted(maps.Keys(got)), want) {
		t.Fatalf("denial counters %v; want one per rpc %v", got, want)
	}
	for rpc, n := range got {
		want := map[string]uint64{"CreateRole": 2, "ListGroups": 1}[rpc]
		if n != want {
			t.Fatalf("denied %s = %d; want %d", rpc, n, want)
		}
	}
	results := map[string]uint64{
		"ok": a.stats.OK.Load(), "denied": a.stats.Denied.Load(), "not_found": a.stats.NotFound.Load(), "conflict": a.stats.Conflict.Load(),
		"bad_request": a.stats.BadRequest.Load(), "error": a.stats.Errors.Load(), "unauthenticated": a.stats.Unauthenticated.Load(),
		"limited": a.stats.Limited.Load(), "unavailable": a.stats.Unavailable.Load(),
	}
	want := map[string]uint64{"ok": 1, "denied": 3, "not_found": 1, "conflict": 2, "bad_request": 1, "error": 1, "unauthenticated": 1, "limited": 0, "unavailable": 0}
	if !maps.Equal(results, want) {
		t.Fatalf("results %v; want %v", results, want)
	}
}

func TestAdminStatsZeroValueHasEveryDenialCounter(t *testing.T) {
	got := (&api.AdminStats{}).DeniedByRPC()
	if len(got) != len(authz.Required) {
		t.Fatalf("counters = %d; want %d", len(got), len(authz.Required))
	}
}
