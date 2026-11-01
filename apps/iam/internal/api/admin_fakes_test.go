package api_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1/iamv1connect"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

type adminCallRecord struct {
	rpc    string
	caller auth.Caller
	args   []any
}

type fakeAdmin struct {
	mu       sync.Mutex
	calls    []adminCallRecord
	errs     map[string]error
	role     store.Role
	group    store.Group
	groups   []store.GroupView
	account  store.AccountView
	next     string
	removals store.RoleRemoval
}

func (f *fakeAdmin) record(rpc string, c auth.Caller, args ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, adminCallRecord{rpc: rpc, caller: c, args: args})
	return f.errs[rpc]
}

func (f *fakeAdmin) setErr(rpc string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[rpc] = err
}

func (f *fakeAdmin) recorded() []adminCallRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]adminCallRecord(nil), f.calls...)
}

func (f *fakeAdmin) CreateRole(_ context.Context, c auth.Caller, in manage.NewRole) (store.Role, error) {
	return f.role, f.record("CreateRole", c, in)
}

func (f *fakeAdmin) UpdateRole(_ context.Context, c auth.Caller, id string, u store.RoleUpdate) (store.Role, error) {
	return f.role, f.record("UpdateRole", c, id, u)
}

func (f *fakeAdmin) DeleteRole(_ context.Context, c auth.Caller, id string) (store.RoleRemoval, error) {
	return f.removals, f.record("DeleteRole", c, id)
}

func (f *fakeAdmin) GetRole(_ context.Context, c auth.Caller, id string) (store.Role, error) {
	return f.role, f.record("GetRole", c, id)
}

func (f *fakeAdmin) ListRoles(_ context.Context, c auth.Caller, token string) ([]store.Role, string, error) {
	return []store.Role{f.role}, f.next, f.record("ListRoles", c, token)
}

func (f *fakeAdmin) AssignAccountRole(_ context.Context, c auth.Caller, accountID, roleID string) error {
	return f.record("AssignAccountRole", c, accountID, roleID)
}

func (f *fakeAdmin) UnassignAccountRole(_ context.Context, c auth.Caller, accountID, roleID string) error {
	return f.record("UnassignAccountRole", c, accountID, roleID)
}

func (f *fakeAdmin) AssignGroupRole(_ context.Context, c auth.Caller, groupID, roleID string) error {
	return f.record("AssignGroupRole", c, groupID, roleID)
}

func (f *fakeAdmin) UnassignGroupRole(_ context.Context, c auth.Caller, groupID, roleID string) error {
	return f.record("UnassignGroupRole", c, groupID, roleID)
}

func (f *fakeAdmin) CreateGroup(_ context.Context, c auth.Caller, in manage.NewGroup) (store.Group, error) {
	return f.group, f.record("CreateGroup", c, in)
}

func (f *fakeAdmin) UpdateGroup(_ context.Context, c auth.Caller, id string, u store.GroupUpdate) (store.Group, error) {
	return f.group, f.record("UpdateGroup", c, id, u)
}

func (f *fakeAdmin) DeleteGroup(_ context.Context, c auth.Caller, id string) error {
	return f.record("DeleteGroup", c, id)
}

func (f *fakeAdmin) ListGroups(_ context.Context, c auth.Caller, token string) ([]store.GroupView, string, error) {
	return f.groups, f.next, f.record("ListGroups", c, token)
}

func (f *fakeAdmin) SetAccountGroups(_ context.Context, c auth.Caller, accountID string, groupIDs []string) error {
	return f.record("SetAccountGroups", c, accountID, groupIDs)
}

func (f *fakeAdmin) GetAccount(_ context.Context, c auth.Caller, id string) (store.AccountView, error) {
	return f.account, f.record("GetAccount", c, id)
}

func (f *fakeAdmin) ListAccounts(_ context.Context, c auth.Caller, token string) ([]store.AccountView, string, error) {
	return []store.AccountView{f.account}, f.next, f.record("ListAccounts", c, token)
}

type adminHarness struct {
	*harness
	admin  *fakeAdmin
	gate   *userlimit.Gate
	stats  *api.AdminStats
	client iamv1connect.AdminServiceClient
}

func newAdminHarness(t *testing.T, mods ...func(*api.Options)) *adminHarness {
	t.Helper()
	gate, err := userlimit.NewGate(8)
	if err != nil {
		t.Fatal(err)
	}
	users, err := userlimit.NewUsers(userlimit.UserConfig{Rate: 1000, Burst: 1000, Inflight: 8, Capacity: 100, Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	a := &adminHarness{admin: &fakeAdmin{}, gate: gate, stats: &api.AdminStats{}}
	list := newTestRevocations(t)
	wire := func(o *api.Options) {
		o.Admin, o.Revocations, o.Claims, o.AdminGate, o.AdminUsers, o.AdminStats = a.admin, list, testClaims, gate, users, a.stats
	}
	a.harness = newHarness(t, append([]func(*api.Options){wire}, mods...)...)
	a.client = iamv1connect.NewAdminServiceClient(http.DefaultClient, a.server.URL)
	return a
}

type adminCall func(a *adminHarness, token string) error

func send[Req, Resp any](token string, msg *Req, call func(context.Context, *connect.Request[Req]) (*connect.Response[Resp], error)) error {
	_, err := call(context.Background(), withToken(token, msg))
	return err
}

var adminCalls = map[string]adminCall{
	"CreateRole": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.CreateRoleRequest{Id: "support", Name: "Support"}, a.client.CreateRole)
	},
	"UpdateRole": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.UpdateRoleRequest{Id: "support", Name: new("Help")}, a.client.UpdateRole)
	},
	"DeleteRole": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.DeleteRoleRequest{RoleId: "support"}, a.client.DeleteRole)
	},
	"GetRole": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.GetRoleRequest{RoleId: "support"}, a.client.GetRole)
	},
	"ListRoles": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.ListRolesRequest{}, a.client.ListRoles)
	},
	"AssignAccountRole": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.AssignAccountRoleRequest{AccountId: "ha", RoleId: "support"}, a.client.AssignAccountRole)
	},
	"UnassignAccountRole": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.UnassignAccountRoleRequest{AccountId: "ha", RoleId: "support"}, a.client.UnassignAccountRole)
	},
	"AssignGroupRole": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.AssignGroupRoleRequest{GroupId: "sales", RoleId: "support"}, a.client.AssignGroupRole)
	},
	"UnassignGroupRole": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.UnassignGroupRoleRequest{GroupId: "sales", RoleId: "support"}, a.client.UnassignGroupRole)
	},
	"CreateGroup": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.CreateGroupRequest{Id: "sales", Name: "Sales"}, a.client.CreateGroup)
	},
	"UpdateGroup": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.UpdateGroupRequest{Id: "sales", Name: new("Sales VN")}, a.client.UpdateGroup)
	},
	"DeleteGroup": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.DeleteGroupRequest{GroupId: "sales"}, a.client.DeleteGroup)
	},
	"ListGroups": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.ListGroupsRequest{}, a.client.ListGroups)
	},
	"SetAccountGroups": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.SetAccountGroupsRequest{AccountId: "ha", GroupIds: []string{"sales"}}, a.client.SetAccountGroups)
	},
	"GetAccount": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.GetAccountRequest{AccountId: "ha"}, a.client.GetAccount)
	},
	"ListAccounts": func(a *adminHarness, tok string) error {
		return send(tok, &iamv1.ListAccountsRequest{}, a.client.ListAccounts)
	},
}
