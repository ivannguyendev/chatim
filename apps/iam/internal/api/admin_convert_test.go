package api_test

import (
	"context"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
)

func lastArgs(t *testing.T, a *adminHarness, rpc string) []any {
	t.Helper()
	calls := a.admin.recorded()
	if len(calls) == 0 || calls[len(calls)-1].rpc != rpc {
		t.Fatalf("last call = %+v; want %s", calls, rpc)
	}
	return calls[len(calls)-1].args
}

func TestAdminConvertsOptionalFields(t *testing.T) {
	a := newAdminHarness(t)
	ctx := context.Background()
	if _, err := a.client.UpdateRole(ctx, withToken(tokenLan, &iamv1.UpdateRoleRequest{Id: "support", Permissions: &iamv1.PermissionList{}})); err != nil {
		t.Fatal(err)
	}
	if u := lastArgs(t, a, "UpdateRole")[1].(store.RoleUpdate); u.Name != nil || u.Permissions == nil || len(*u.Permissions) != 0 {
		t.Fatalf("present empty permissions = %+v; want replace with none", u)
	}
	if _, err := a.client.UpdateRole(ctx, withToken(tokenLan, &iamv1.UpdateRoleRequest{Id: "support", Name: new("Help")})); err != nil {
		t.Fatal(err)
	}
	if u := lastArgs(t, a, "UpdateRole")[1].(store.RoleUpdate); u.Permissions != nil || u.Name == nil || *u.Name != "Help" {
		t.Fatalf("absent permissions = %+v; want keep", u)
	}
	if _, err := a.client.UpdateGroup(ctx, withToken(tokenLan, &iamv1.UpdateGroupRequest{Id: "sales", ParentId: new(""), Mentionable: new(false)})); err != nil {
		t.Fatal(err)
	}
	if u := lastArgs(t, a, "UpdateGroup")[1].(store.GroupUpdate); u.ParentID == nil || *u.ParentID != "" || u.Name != nil || u.Mentionable == nil || *u.Mentionable {
		t.Fatalf("present empty parent = %+v; want move to root", u)
	}
	if _, err := a.client.UpdateGroup(ctx, withToken(tokenLan, &iamv1.UpdateGroupRequest{Id: "sales", Name: new("Sales VN")})); err != nil {
		t.Fatal(err)
	}
	if u := lastArgs(t, a, "UpdateGroup")[1].(store.GroupUpdate); u.ParentID != nil || u.Mentionable != nil {
		t.Fatalf("absent parent = %+v; want keep", u)
	}
	if _, err := a.client.CreateGroup(ctx, withToken(tokenLan, &iamv1.CreateGroupRequest{Id: "sales", Name: "Sales", ParentId: "shop"})); err != nil {
		t.Fatal(err)
	}
	if in := lastArgs(t, a, "CreateGroup")[0].(manage.NewGroup); in.Mentionable != nil || in.ParentID != "shop" {
		t.Fatalf("create group = %+v; want mentionable unset", in)
	}
}

func TestAdminPassesTheCallerAndShapesResponses(t *testing.T) {
	a := newAdminHarness(t)
	ctx := context.Background()
	a.admin.role = store.Role{ID: "support", Name: "Support", Ver: 3, Permissions: []string{"iam:groups.read"}, CreatedAt: t0, UpdatedAt: t0, UpdatedBy: "lan"}
	a.admin.group = store.Group{ID: "sales", Name: "Sales", ParentID: "shop", Mentionable: true}
	a.admin.groups = []store.GroupView{{Group: a.admin.group, RoleIDs: []string{"support"}}}
	a.admin.account = store.AccountView{ID: "ha", Username: "ha", State: store.StateDisabled, GroupIDs: []string{"sales"}, CreatedAt: t0}
	a.admin.removals = store.RoleRemoval{Accounts: 2, Groups: 1}
	a.admin.next = "sales"
	req := withToken(tokenLan, &iamv1.GetRoleRequest{RoleId: "support"})
	req.Header().Set("X-Real-IP", "198.51.100.7")
	role, err := a.client.GetRole(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if c := a.admin.recorded()[0].caller; c != (auth.Caller{Tenant: "shop", Account: "lan", Session: "s-lan", IP: "198.51.100.7"}) {
		t.Fatalf("caller = %+v", c)
	}
	r := role.Msg.GetRole()
	if r.GetId() != "support" || r.GetVer() != 3 || !slices.Equal(r.GetPermissions(), []string{"iam:groups.read"}) || !r.GetCreatedAt().AsTime().Equal(t0) || r.GetUpdatedBy() != "lan" {
		t.Fatalf("role = %+v", r)
	}
	created, err := a.client.CreateGroup(ctx, withToken(tokenLan, &iamv1.CreateGroupRequest{Id: "sales", Name: "Sales"}))
	if err != nil || created.Msg.GetGroup().GetParentId() != "shop" || len(created.Msg.GetGroup().GetRoleIds()) != 0 {
		t.Fatalf("created group = %+v, %v; want no role ids", created, err)
	}
	groups, err := a.client.ListGroups(ctx, withToken(tokenLan, &iamv1.ListGroupsRequest{PageToken: "eng"}))
	if err != nil || groups.Msg.GetNextPageToken() != "sales" || !slices.Equal(groups.Msg.GetGroups()[0].GetRoleIds(), []string{"support"}) {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	if token := lastArgs(t, a, "ListGroups")[0]; token != "eng" {
		t.Fatalf("page token = %v", token)
	}
	account, err := a.client.GetAccount(ctx, withToken(tokenLan, &iamv1.GetAccountRequest{AccountId: "ha"}))
	if err != nil || account.Msg.GetAccount().GetState() != iamv1.UserState_USER_STATE_DISABLED || !slices.Equal(account.Msg.GetAccount().GetGroupIds(), []string{"sales"}) {
		t.Fatalf("account = %+v, %v", account, err)
	}
	removed, err := a.client.DeleteRole(ctx, withToken(tokenLan, &iamv1.DeleteRoleRequest{RoleId: "support"}))
	if err != nil || removed.Msg.GetAccountsUnassigned() != 2 || removed.Msg.GetGroupsUnassigned() != 1 {
		t.Fatalf("delete role = %+v, %v", removed, err)
	}
	if err := adminCalls["SetAccountGroups"](a, tokenLan); err != nil {
		t.Fatal(err)
	}
	if args := lastArgs(t, a, "SetAccountGroups"); args[0] != "ha" || !slices.Equal(args[1].([]string), []string{"sales"}) {
		t.Fatalf("set account groups args = %v", args)
	}
}
