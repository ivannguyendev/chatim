package pgstore_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func newRole(tenant, id string, perms ...string) store.Role {
	return store.Role{TenantID: tenant, ID: id, Name: "Role " + id, Permissions: perms, CreatedAt: t0, UpdatedAt: t0, UpdatedBy: "test"}
}

func createRole(t *testing.T, s fixture, r store.Role) {
	t.Helper()
	if err := s.CreateRole(context.Background(), r, audit("role_create")); err != nil {
		t.Fatalf("CreateRole %s/%s: %v", r.TenantID, r.ID, err)
	}
}

func inAdmin(s fixture, fn func(ctx context.Context, a store.AdminTx) error) error {
	ctx := context.Background()
	return s.RunAdmin(ctx, store.AdminTxOptions{}, func(a store.AdminTx) error { return fn(ctx, a) })
}

func roleOf(t *testing.T, s fixture, tenant, id string) store.Role {
	t.Helper()
	var r store.Role
	err := inAdmin(s, func(ctx context.Context, a store.AdminTx) error {
		var err error
		r, err = a.Role(ctx, tenant, id)
		return err
	})
	if err != nil {
		t.Fatalf("Role %s/%s: %v", tenant, id, err)
	}
	return r
}

func wantStrings(t *testing.T, step string, got []string, want ...string) {
	t.Helper()
	if got == nil || !slices.Equal(got, want) {
		t.Fatalf("%s = %#v; want %v", step, got, want)
	}
}

func TestCreateRoleRejectsUnknownPermission(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	ctx := context.Background()

	err := s.CreateRole(ctx, newRole("shop", "support", permGroupsRead, "iam:acounts.read", "iam:zz.read"), audit("role_create"))
	wantErr(t, err, apperr.ErrInvalidArgument, "cli create")
	if !strings.Contains(err.Error(), "permission not in catalog: iam:acounts.read, iam:zz.read") {
		t.Fatalf("cli create: err = %v; want both unknown permissions named", err)
	}
	err = inAdmin(s, func(ctx context.Context, a store.AdminTx) error {
		return a.CreateRole(ctx, newRole("shop", "support", "iam:acounts.read"))
	})
	wantErr(t, err, apperr.ErrInvalidArgument, "tx create")
	if got := queryStrings(t, s.raw, "SELECT id FROM roles WHERE tenant_id = 'shop' AND id = 'support'"); len(got) != 0 {
		t.Fatalf("role rows after refusal = %v; want none", got)
	}
	wantAudits(t, s, "role_create", 0)

	err = inAdmin(s, func(ctx context.Context, a store.AdminTx) error {
		_, _, err := a.UpdateRole(ctx, "shop", "missing", store.RoleUpdate{}, t0, "test")
		return err
	})
	wantErr(t, err, apperr.ErrNotFound, "update missing role")
}

func TestSameRoleIDInTwoTenants(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedTenant(t, s, "mall")
	createRole(t, s, newRole("shop", "support", permGroupsRead))
	createRole(t, s, newRole("mall", "support", permRolesRead, permAccountsRead))

	shop := roleOf(t, s, "shop", "support")
	wantStrings(t, "shop permissions", shop.Permissions, permGroupsRead)
	if shop.Builtin || shop.Ver != 1 || shop.Name != "Role support" {
		t.Fatalf("shop role = %+v; want custom role at ver 1", shop)
	}
	wantStrings(t, "mall permissions", roleOf(t, s, "mall", "support").Permissions, permAccountsRead, permRolesRead)

	err := s.CreateRole(context.Background(), newRole("shop", "support"), audit("role_create"))
	wantErr(t, err, apperr.ErrAlreadyExists, "duplicate in one tenant")
	err = s.CreateRole(context.Background(), newRole("ghost", "support"), audit("role_create"))
	wantErr(t, err, apperr.ErrNotFound, "unknown tenant")

	if _, err := s.DeleteRole(context.Background(), "shop", "support", audit("role_delete")); err != nil {
		t.Fatalf("DeleteRole shop: %v", err)
	}
	wantStrings(t, "mall after shop delete", roleOf(t, s, "mall", "support").Permissions, permAccountsRead, permRolesRead)
	roles, err := s.ListRoles(context.Background(), "mall")
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(roles) != 2 || roles[0].ID != "support" || roles[1].ID != "tenant-admin" {
		t.Fatalf("mall roles = %+v; want support and tenant-admin", roles)
	}
}

func TestUpdateRoleReplacesPermissionsAndReturnsAdded(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	createRole(t, s, newRole("shop", "support", permGroupsRead, permRolesRead))

	name := "Support desk"
	perms := []string{permRolesRead, permAccountsRead, permGroupsWrite}
	var got store.Role
	var added []string
	err := inAdmin(s, func(ctx context.Context, a store.AdminTx) error {
		var err error
		got, added, err = a.UpdateRole(ctx, "shop", "support", store.RoleUpdate{Name: &name, Permissions: &perms}, t0, "lan")
		return err
	})
	if err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	wantStrings(t, "added", added, permAccountsRead, permGroupsWrite)
	wantStrings(t, "permissions", got.Permissions, permAccountsRead, permGroupsWrite, permRolesRead)
	if got.Name != name || got.Ver != 2 || got.UpdatedBy != "lan" {
		t.Fatalf("updated role = %+v; want new name, ver 2, by lan", got)
	}

	err = s.UpdateRole(context.Background(), "shop", "support", store.RoleUpdate{Permissions: &[]string{"iam:acounts.read"}}, audit("role_update"))
	wantErr(t, err, apperr.ErrInvalidArgument, "unknown permission on update")
	wantStrings(t, "permissions after refusal", roleOf(t, s, "shop", "support").Permissions, permAccountsRead, permGroupsWrite, permRolesRead)

	if err := s.UpdateRole(context.Background(), "shop", "support", store.RoleUpdate{Permissions: &[]string{}}, audit("role_update")); err != nil {
		t.Fatalf("UpdateRole to empty: %v", err)
	}
	cleared := roleOf(t, s, "shop", "support")
	wantStrings(t, "permissions after clearing", cleared.Permissions)
	if cleared.Name != name || cleared.Ver != 3 {
		t.Fatalf("cleared role = %+v; want name kept and ver 3", cleared)
	}
	wantAudits(t, s, "role_update", 1)
}

func TestBuiltinRoleCannotBeUpdatedOrDeleted(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	ctx := context.Background()
	name := "Owner"
	perms := []string{permRolesRead}

	err := s.UpdateRole(ctx, "shop", "tenant-admin", store.RoleUpdate{Name: &name}, audit("role_update"))
	wantErr(t, err, apperr.ErrFailedPrecondition, "cli update")
	if !strings.Contains(err.Error(), "builtin role cannot change") {
		t.Fatalf("cli update: err = %v; want the builtin message", err)
	}
	_, err = s.DeleteRole(ctx, "shop", "tenant-admin", audit("role_delete"))
	wantErr(t, err, apperr.ErrFailedPrecondition, "cli delete")
	err = inAdmin(s, func(ctx context.Context, a store.AdminTx) error {
		_, _, err := a.UpdateRole(ctx, "shop", "tenant-admin", store.RoleUpdate{Permissions: &perms}, t0, "test")
		return err
	})
	wantErr(t, err, apperr.ErrFailedPrecondition, "tx update")
	err = inAdmin(s, func(ctx context.Context, a store.AdminTx) error {
		_, err := a.DeleteRole(ctx, "shop", "tenant-admin")
		return err
	})
	wantErr(t, err, apperr.ErrFailedPrecondition, "tx delete")

	admin := roleOf(t, s, "shop", "tenant-admin")
	if !admin.Builtin || admin.Ver != 1 || admin.Name != "Tenant admin" || len(admin.Permissions) != 0 {
		t.Fatalf("tenant-admin = %+v; want it unchanged", admin)
	}
	wantAudits(t, s, "role_update", 0)
	wantAudits(t, s, "role_delete", 0)

	var grant store.Grant
	err = inAdmin(s, func(ctx context.Context, a store.AdminTx) error {
		var err error
		grant, err = a.RoleGrant(ctx, "shop", "tenant-admin")
		return err
	})
	if err != nil || !grant.TenantAdmin {
		t.Fatalf("RoleGrant tenant-admin = %+v, %v; want TenantAdmin", grant, err)
	}
}

func TestClientPermissionsSetRefusesIAMAndInUse(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	ctx := context.Background()
	entry := func(p, d string) store.ClientPermission { return store.ClientPermission{Permission: p, Description: d} }

	err := s.SetClientPermissions(ctx, "iam", []store.ClientPermission{entry("iam:roles.read", "x")}, audit("client_permissions_set"))
	wantErr(t, err, apperr.ErrFailedPrecondition, "iam client")
	err = s.SetClientPermissions(ctx, "chatim", []store.ClientPermission{entry("iam:rooms.read", "x")}, audit("client_permissions_set"))
	wantErr(t, err, apperr.ErrInvalidArgument, "foreign prefix")
	err = s.SetClientPermissions(ctx, "chatim", []store.ClientPermission{entry("chatim:rooms.read", "x"), entry("chatim:rooms.read", "y")}, audit("client_permissions_set"))
	wantErr(t, err, apperr.ErrInvalidArgument, "listed twice")

	set := []store.ClientPermission{entry("chatim:rooms.read", "Read rooms"), entry("chatim:rooms.write", "Write rooms")}
	if err := s.SetClientPermissions(ctx, "chatim", set, audit("client_permissions_set")); err != nil {
		t.Fatalf("SetClientPermissions: %v", err)
	}
	createRole(t, s, newRole("shop", "reader", "chatim:rooms.read"))

	err = s.SetClientPermissions(ctx, "chatim", []store.ClientPermission{entry("chatim:rooms.write", "Write rooms")}, audit("client_permissions_set"))
	wantErr(t, err, apperr.ErrFailedPrecondition, "removing a used permission")
	if !strings.Contains(err.Error(), "permission in use: chatim:rooms.read") {
		t.Fatalf("in use: err = %v; want the permission named", err)
	}

	keep := []store.ClientPermission{entry("chatim:rooms.read", "Read any room"), entry("chatim:members.read", "Read members")}
	if err := s.SetClientPermissions(ctx, "chatim", keep, audit("client_permissions_set")); err != nil {
		t.Fatalf("SetClientPermissions replace: %v", err)
	}
	got := queryStrings(t, s.raw, "SELECT permission || '=' || description FROM client_permissions WHERE client_id = 'chatim' ORDER BY permission")
	wantStrings(t, "chatim catalog", got, "chatim:members.read=Read members", "chatim:rooms.read=Read any room")
	wantStrings(t, "iam catalog size", queryStrings(t, s.raw, "SELECT count(*)::text FROM client_permissions WHERE client_id = 'iam'"), "12")
	wantAudits(t, s, "client_permissions_set", 2)
}
