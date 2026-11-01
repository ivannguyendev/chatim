package pgstore_test

import (
	"context"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const (
	permGroupsRead   = "iam:groups.read"
	permGroupsWrite  = "iam:groups.write"
	permRolesWrite   = "iam:roles.write"
	permRolesRead    = "iam:roles.read"
	permAccountsRead = "iam:accounts.read"
	permMachines     = "iam:machines.write"
)

func seedRole(t *testing.T, s fixture, tenant, id string, perms ...string) {
	t.Helper()
	exec(t, s.raw, "INSERT INTO roles (tenant_id, id, name, builtin, ver, created_at, updated_at, updated_by) VALUES ($1, $2, $2, false, 1, now(), now(), 'test')", tenant, id)
	exec(t, s.raw, "INSERT INTO role_permissions (tenant_id, role_id, permission) SELECT $1, $2, unnest($3::text[])", tenant, id, perms)
}

func bindGroupRole(t *testing.T, s fixture, tenant, group, role string) {
	t.Helper()
	exec(t, s.raw, "INSERT INTO group_roles (tenant_id, group_id, role_id, created_at, created_by) VALUES ($1, $2, $3, now(), 'test')", tenant, group, role)
}

func bindAccountRole(t *testing.T, s fixture, tenant, account, role string) {
	t.Helper()
	exec(t, s.raw, "INSERT INTO account_roles (tenant_id, account_id, role_id, created_at, created_by) VALUES ($1, $2, $3, now(), 'test')", tenant, account, role)
}

func tenantGroup(tenant, id, parent string) store.Group {
	g := group(id, parent)
	g.TenantID = tenant
	return g
}

func joinGroups(t *testing.T, s fixture, tenant, account string, groups ...string) {
	t.Helper()
	if err := s.SetAccountGroups(context.Background(), tenant, account, groups, audit("account_groups_set")); err != nil {
		t.Fatalf("SetAccountGroups %s/%s: %v", tenant, account, err)
	}
}

func readGrant(t *testing.T, s fixture, read func(ctx context.Context, a store.AdminTx) (store.Grant, error)) store.Grant {
	t.Helper()
	ctx := context.Background()
	var g store.Grant
	err := s.RunAdmin(ctx, store.AdminTxOptions{ReadOnly: true}, func(a store.AdminTx) error {
		var err error
		g, err = read(ctx, a)
		return err
	})
	if err != nil {
		t.Fatalf("read grant: %v", err)
	}
	return g
}

func accountGrant(t *testing.T, s fixture, tenant, account string) store.Grant {
	t.Helper()
	return readGrant(t, s, func(ctx context.Context, a store.AdminTx) (store.Grant, error) {
		return a.AccountGrant(ctx, tenant, account)
	})
}

func groupsGrant(t *testing.T, s fixture, tenant string, ids ...string) store.Grant {
	t.Helper()
	return readGrant(t, s, func(ctx context.Context, a store.AdminTx) (store.Grant, error) {
		return a.GroupsGrant(ctx, tenant, ids)
	})
}

func wantGrant(t *testing.T, step string, got store.Grant, tenantAdmin bool, perms ...string) {
	t.Helper()
	if got.Permissions == nil || got.TenantAdmin != tenantAdmin || !slices.Equal(got.Permissions, perms) {
		t.Fatalf("%s: grant = %+v; want tenant admin %v with %v (never nil)", step, got, tenantAdmin, perms)
	}
}

func moveGroup(t *testing.T, s fixture, tenant, id, parent string) {
	t.Helper()
	ctx := context.Background()
	err := s.RunAdmin(ctx, store.AdminTxOptions{Tenant: tenant, TreeLock: true}, func(a store.AdminTx) error {
		return a.UpdateGroup(ctx, tenant, id, moveUnder(parent), t0, "test")
	})
	if err != nil {
		t.Fatalf("move %s/%s under %s: %v", tenant, id, parent, err)
	}
}

func TestGrandchildInheritsTheRootRole(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", "binh", "binh")
	createGroups(t, s, group("cong-ty", ""), group("it", "cong-ty"), group("it-hcm", "it"))
	seedRole(t, s, "shop", "viewer", permGroupsRead)
	bindGroupRole(t, s, "shop", "cong-ty", "viewer")
	joinGroups(t, s, "shop", "binh", "it-hcm")
	wantGrant(t, "binh", accountGrant(t, s, "shop", "binh"), false, permGroupsRead)
	wantGrant(t, "it-hcm chain", groupsGrant(t, s, "shop", "it-hcm"), false, permGroupsRead)
	wantGrant(t, "it chain", groupsGrant(t, s, "shop", "it"), false, permGroupsRead)
	wantGrant(t, "no groups", groupsGrant(t, s, "shop"), false)
}

func TestMovingAGroupChangesTheGrant(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", "binh", "binh")
	createGroups(t, s, group("cong-ty", ""), group("it", "cong-ty"), group("it-hcm", "it"), group("hr", ""))
	seedRole(t, s, "shop", "it-admin", permGroupsWrite)
	seedRole(t, s, "shop", "hr-admin", permRolesWrite)
	bindGroupRole(t, s, "shop", "it", "it-admin")
	bindGroupRole(t, s, "shop", "hr", "hr-admin")
	joinGroups(t, s, "shop", "binh", "it-hcm")
	wantGrant(t, "before the move", accountGrant(t, s, "shop", "binh"), false, permGroupsWrite)
	moveGroup(t, s, "shop", "it-hcm", "hr")
	wantGrant(t, "after the move", accountGrant(t, s, "shop", "binh"), false, permRolesWrite)
	wantGrant(t, "it-hcm chain", groupsGrant(t, s, "shop", "it-hcm"), false, permRolesWrite)
	err := s.RunAdmin(ctx, store.AdminTxOptions{}, func(a store.AdminTx) error {
		n, err := a.GroupNode(ctx, "shop", "it-hcm")
		if err != nil {
			return err
		}
		if n.ParentID != "hr" || !slices.Equal(n.Ancestors, []string{"hr"}) {
			t.Errorf("node = parent %q ancestors %v; want hr, [hr]", n.ParentID, n.Ancestors)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GroupNode: %v", err)
	}
}

func TestAccountGrantUnionsDirectAndGroupRoles(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", "binh", "binh")
	seedAccount(t, s, "shop", "lan", "lan")
	createGroups(t, s, group("sales", ""), group("old", ""))
	seedRole(t, s, "shop", "reader", permAccountsRead, permGroupsRead)
	seedRole(t, s, "shop", "writer", permGroupsRead, permGroupsWrite)
	seedRole(t, s, "shop", "machines", permMachines)
	seedRole(t, s, "shop", "empty")
	bindAccountRole(t, s, "shop", "binh", "reader")
	bindAccountRole(t, s, "shop", "binh", "empty")
	bindGroupRole(t, s, "shop", "sales", "writer")
	bindGroupRole(t, s, "shop", "old", "machines")
	joinGroups(t, s, "shop", "binh", "old", "sales")
	exec(t, s.raw, "UPDATE groups SET state = 2 WHERE tenant_id = 'shop' AND id = 'old'")
	wantGrant(t, "binh", accountGrant(t, s, "shop", "binh"), false, permAccountsRead, permGroupsRead, permGroupsWrite)
	wantGrant(t, "deleted group", groupsGrant(t, s, "shop", "old"), false)
	wantGrant(t, "lan holds nothing", accountGrant(t, s, "shop", "lan"), false)
	wantGrant(t, "unknown account", accountGrant(t, s, "shop", "ghost"), false)
}

func TestTenantAdminFlagHasNoPermissionRows(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", "binh", "binh")
	seedAccount(t, s, "shop", "lan", "lan")
	createGroups(t, s, group("admins", ""), group("admins-hcm", "admins"))
	seedRole(t, s, "shop", "viewer", permGroupsRead)
	bindAccountRole(t, s, "shop", "binh", "tenant-admin")
	bindGroupRole(t, s, "shop", "admins", "tenant-admin")
	bindGroupRole(t, s, "shop", "admins-hcm", "viewer")
	joinGroups(t, s, "shop", "lan", "admins-hcm")
	if n := len(queryStrings(t, s.raw, "SELECT permission FROM role_permissions WHERE role_id = 'tenant-admin'")); n != 0 {
		t.Fatalf("tenant-admin permission rows = %d; want 0", n)
	}
	wantGrant(t, "binh direct", accountGrant(t, s, "shop", "binh"), true)
	wantGrant(t, "lan through an ancestor", accountGrant(t, s, "shop", "lan"), true, permGroupsRead)
	wantGrant(t, "admins-hcm chain", groupsGrant(t, s, "shop", "admins-hcm"), true, permGroupsRead)
}

func TestGrantStaysInsideTheTenant(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	for _, tenant := range []string{"shop", "mall"} {
		seedTenant(t, s, tenant)
		seedAccount(t, s, tenant, "binh", "binh")
		for _, g := range []store.Group{tenantGroup(tenant, "it", ""), tenantGroup(tenant, "it-hcm", "it"), tenantGroup(tenant, "hr", "")} {
			if err := s.CreateGroup(ctx, g, audit("group_create")); err != nil {
				t.Fatalf("CreateGroup %s/%s: %v", tenant, g.ID, err)
			}
		}
		joinGroups(t, s, tenant, "binh", "it-hcm")
	}
	if err := s.CreateGroup(ctx, tenantGroup("mall", "mall-only", ""), audit("group_create")); err != nil {
		t.Fatalf("CreateGroup mall-only: %v", err)
	}
	seedRole(t, s, "shop", "viewer", permGroupsRead)
	seedRole(t, s, "mall", "viewer", permRolesRead)
	seedRole(t, s, "mall", "auditor", permAccountsRead)
	seedRole(t, s, "mall", "hr-admin", permRolesWrite)
	bindGroupRole(t, s, "shop", "it", "viewer")
	bindGroupRole(t, s, "mall", "it", "viewer")
	bindGroupRole(t, s, "mall", "hr", "hr-admin")
	bindGroupRole(t, s, "mall", "mall-only", "hr-admin")
	bindAccountRole(t, s, "mall", "binh", "auditor")
	bindAccountRole(t, s, "mall", "binh", "tenant-admin")
	wantGrant(t, "shop binh", accountGrant(t, s, "shop", "binh"), false, permGroupsRead)
	wantGrant(t, "shop chain with foreign and unknown ids", groupsGrant(t, s, "shop", "it-hcm", "mall-only", "ghost"), false, permGroupsRead)
	wantGrant(t, "mall binh", accountGrant(t, s, "mall", "binh"), true, permAccountsRead, permRolesRead)
	moveGroup(t, s, "shop", "it-hcm", "hr")
	wantAncestorsIn(t, s, "shop", map[string][]string{"it-hcm": {"hr"}})
	wantAncestorsIn(t, s, "mall", map[string][]string{"it-hcm": {"it"}, "hr": {}})
	wantGrant(t, "shop binh after the move", accountGrant(t, s, "shop", "binh"), false)
	wantGrant(t, "mall binh after the shop move", accountGrant(t, s, "mall", "binh"), true, permAccountsRead, permRolesRead)
}

func wantAncestorsIn(t *testing.T, s fixture, tenant string, want map[string][]string) {
	t.Helper()
	for id, path := range want {
		got := queryStrings(t, s.raw, "SELECT unnest(ancestors) FROM groups WHERE tenant_id = $1 AND id = $2", tenant, id)
		if !slices.Equal(got, path) && len(got)+len(path) > 0 {
			t.Fatalf("ancestors of %s/%s = %v; want %v", tenant, id, got, path)
		}
	}
}
