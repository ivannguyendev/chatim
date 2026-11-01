package pgstore_test

import (
	"context"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func assign(s fixture, account, group, role string) (bool, error) {
	var added bool
	err := inAdmin(s, func(ctx context.Context, a store.AdminTx) error {
		var err error
		if group != "" {
			added, err = a.AssignGroupRole(ctx, "shop", group, role, t0, "test")
		} else {
			added, err = a.AssignAccountRole(ctx, "shop", account, role, t0, "test")
		}
		return err
	})
	return added, err
}

func unassign(s fixture, account, group, role string) (bool, error) {
	var removed bool
	err := inAdmin(s, func(ctx context.Context, a store.AdminTx) error {
		var err error
		if group != "" {
			removed, err = a.UnassignGroupRole(ctx, "shop", group, role)
		} else {
			removed, err = a.UnassignAccountRole(ctx, "shop", account, role)
		}
		return err
	})
	return removed, err
}

func wantFlag(t *testing.T, step string, got bool, err error, want bool) {
	t.Helper()
	if err != nil || got != want {
		t.Fatalf("%s = %v, %v; want %v", step, got, err, want)
	}
}

func rolesOfAccount(t *testing.T, s fixture, account string) []string {
	t.Helper()
	return queryStrings(t, s.raw, "SELECT role_id FROM account_roles WHERE tenant_id = 'shop' AND account_id = $1 ORDER BY role_id", account)
}

func TestDeletingARoleRemovesItsBindings(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", "binh", "binh")
	seedAccount(t, s, "shop", "lan", "lan")
	createGroups(t, s, group("sales", ""))
	createRole(t, s, newRole("shop", "support", permGroupsRead, permRolesRead))
	createRole(t, s, newRole("shop", "viewer", permGroupsRead))
	for _, b := range [][3]string{{"binh", "", "support"}, {"lan", "", "support"}, {"", "sales", "support"}, {"lan", "", "viewer"}} {
		if _, err := assign(s, b[0], b[1], b[2]); err != nil {
			t.Fatalf("assign %v: %v", b, err)
		}
	}

	removed, err := s.DeleteRole(context.Background(), "shop", "support", audit("role_delete"))
	if err != nil || removed != (store.RoleRemoval{Accounts: 2, Groups: 1}) {
		t.Fatalf("DeleteRole = %+v, %v; want 2 accounts and 1 group", removed, err)
	}
	for _, table := range []string{"account_roles", "group_roles", "role_permissions", "roles"} {
		column := map[string]string{"roles": "id"}[table]
		if column == "" {
			column = "role_id"
		}
		if got := queryStrings(t, s.raw, "SELECT tenant_id FROM "+table+" WHERE tenant_id = 'shop' AND "+column+" = 'support'"); len(got) != 0 {
			t.Fatalf("%s rows of support = %d; want 0", table, len(got))
		}
	}
	wantStrings(t, "lan keeps viewer", rolesOfAccount(t, s, "lan"), "viewer")
	detail := queryStrings(t, s.raw, "SELECT (detail->>'accounts') || '/' || (detail->>'groups') FROM audit_events WHERE action = 'role_delete'")
	wantStrings(t, "role_delete audit", detail, "2/1")

	err = inAdmin(s, func(ctx context.Context, a store.AdminTx) error {
		_, err := a.DeleteRole(ctx, "shop", "support")
		return err
	})
	wantErr(t, err, apperr.ErrNotFound, "delete twice")
}

func TestAssignIsIdempotentAndUnassignOfAbsentIsANoop(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", "binh", "binh")
	createGroups(t, s, group("sales", ""))
	createRole(t, s, newRole("shop", "support", permGroupsRead))

	added, err := assign(s, "binh", "", "support")
	wantFlag(t, "first account assign", added, err, true)
	added, err = assign(s, "binh", "", "support")
	wantFlag(t, "second account assign", added, err, false)
	added, err = assign(s, "", "sales", "support")
	wantFlag(t, "first group assign", added, err, true)
	added, err = assign(s, "", "sales", "support")
	wantFlag(t, "second group assign", added, err, false)
	wantStrings(t, "account roles", rolesOfAccount(t, s, "binh"), "support")

	removed, err := unassign(s, "binh", "", "support")
	wantFlag(t, "first account unassign", removed, err, true)
	removed, err = unassign(s, "binh", "", "support")
	wantFlag(t, "second account unassign", removed, err, false)
	removed, err = unassign(s, "", "sales", "support")
	wantFlag(t, "first group unassign", removed, err, true)
	removed, err = unassign(s, "", "sales", "support")
	wantFlag(t, "second group unassign", removed, err, false)

	_, err = assign(s, "ghost", "", "support")
	wantErr(t, err, apperr.ErrNotFound, "unknown account")
	_, err = assign(s, "binh", "", "ghost")
	wantErr(t, err, apperr.ErrNotFound, "unknown role on account")
	_, err = assign(s, "", "sales", "ghost")
	wantErr(t, err, apperr.ErrNotFound, "unknown role on group")
	_, err = assign(s, "", "ghost", "support")
	wantErr(t, err, apperr.ErrNotFound, "unknown group")
}

func TestBindingToADeletedGroupIsNotFound(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	createGroups(t, s, group("sales", ""))
	createRole(t, s, newRole("shop", "support", permGroupsRead))
	if err := s.DeleteGroup(context.Background(), "shop", "sales", audit("group_delete")); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	_, err := assign(s, "", "sales", "support")
	wantErr(t, err, apperr.ErrNotFound, "assign")
	_, err = unassign(s, "", "sales", "support")
	wantErr(t, err, apperr.ErrNotFound, "unassign")
	err = s.SetGroupRoles(context.Background(), "shop", "sales", []string{"support"}, audit("group_roles_set"))
	wantErr(t, err, apperr.ErrNotFound, "set group roles")
	if got := queryStrings(t, s.raw, "SELECT role_id FROM group_roles WHERE tenant_id = 'shop'"); len(got) != 0 {
		t.Fatalf("group_roles = %v; want none", got)
	}
	wantAudits(t, s, "group_roles_set", 0)
}

func TestCreateAccountWithRoleIsAtomic(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	ctx := context.Background()
	a := store.Account{
		TenantID: "shop", ID: "ha", Username: "ha", State: store.StateActive, PasswordHash: "hash",
		PasswordMustChange: true, PasswordChangedAt: t0, Ver: 1, CreatedAt: t0, UpdatedAt: t0, UpdatedBy: "admin-cli",
	}
	at := store.AuditEvent{TenantID: "shop", At: t0, Actor: "admin-cli", Action: "account_create", AccountID: "ha"}

	err := s.CreateAccountWithRole(ctx, a, "ghost", at)
	wantErr(t, err, apperr.ErrNotFound, "unknown role")
	if _, err := s.AccountByID(ctx, "shop", "ha"); err == nil {
		t.Fatal("account exists after a refused create")
	}
	wantAudits(t, s, "account_create", 0)

	if err := s.CreateAccountWithRole(ctx, a, "tenant-admin", at); err != nil {
		t.Fatalf("CreateAccountWithRole: %v", err)
	}
	wantStrings(t, "roles of ha", rolesOfAccount(t, s, "ha"), "tenant-admin")
	if g := accountGrant(t, s, "shop", "ha"); !g.TenantAdmin {
		t.Fatalf("grant of ha = %+v; want tenant-admin", g)
	}
	wantAudits(t, s, "account_create", 1)
	detail := queryStrings(t, s.raw, "SELECT account_id || '/' || (detail->>'role') FROM audit_events WHERE action = 'account_role_assign'")
	wantStrings(t, "assign audit", detail, "ha/tenant-admin")

	a.ID = "ha2"
	err = s.CreateAccountWithRole(ctx, a, "tenant-admin", at)
	wantErr(t, err, apperr.ErrAlreadyExists, "duplicate username")
	wantStrings(t, "roles of ha2", rolesOfAccount(t, s, "ha2"))
	wantAudits(t, s, "account_role_assign", 1)
}

func TestSetAccountRolesReplacesTheWholeList(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", "binh", "binh")
	createGroups(t, s, group("sales", ""))
	for _, id := range []string{"a", "b", "c"} {
		createRole(t, s, newRole("shop", id, permGroupsRead))
	}
	ctx := context.Background()
	set := func(roles ...string) error {
		return s.SetAccountRoles(ctx, "shop", "binh", roles, audit("account_roles_set"))
	}

	if err := set("b", "a"); err != nil {
		t.Fatalf("set a,b: %v", err)
	}
	wantStrings(t, "after a,b", rolesOfAccount(t, s, "binh"), "a", "b")
	if err := set("c", "b"); err != nil {
		t.Fatalf("set b,c: %v", err)
	}
	wantStrings(t, "after b,c", rolesOfAccount(t, s, "binh"), "b", "c")
	wantErr(t, set("a", "a"), apperr.ErrInvalidArgument, "listed twice")
	wantErr(t, set("a", "ghost"), apperr.ErrNotFound, "unknown role")
	wantStrings(t, "after refusals", rolesOfAccount(t, s, "binh"), "b", "c")
	wantErr(t, s.SetAccountRoles(ctx, "shop", "ghost", []string{"a"}, audit("account_roles_set")), apperr.ErrNotFound, "unknown account")
	if err := set(); err != nil {
		t.Fatalf("set none: %v", err)
	}
	wantStrings(t, "after none", rolesOfAccount(t, s, "binh"))
	wantAudits(t, s, "account_roles_set", 3)

	if err := s.SetGroupRoles(ctx, "shop", "sales", []string{"c", "a"}, audit("group_roles_set")); err != nil {
		t.Fatalf("SetGroupRoles: %v", err)
	}
	if err := s.SetGroupRoles(ctx, "shop", "sales", []string{"b"}, audit("group_roles_set")); err != nil {
		t.Fatalf("SetGroupRoles b: %v", err)
	}
	wantStrings(t, "group roles", queryStrings(t, s.raw, "SELECT role_id FROM group_roles WHERE tenant_id = 'shop' AND group_id = 'sales'"), "b")
}

func seedNumberedAccounts(t *testing.T, s fixture, tenant string, from, to int) {
	t.Helper()
	exec(t, s.raw, `INSERT INTO accounts (tenant_id, id, username, display_name, state, password_hash, password_must_change, password_changed_at, ver, created_at, updated_at, updated_by)
SELECT $1, 'u' || lpad(i::text, 3, '0'), 'user' || i, 'User ' || i, 1, 'hash', false, $4, 1, $4, $4, 'test' FROM generate_series($2::int, $3::int) AS i`,
		tenant, from, to, t0)
}

func TestListsPageByID(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedTenant(t, s, "mall")
	seedNumberedAccounts(t, s, "shop", 1, 205)
	seedNumberedAccounts(t, s, "mall", 0, 300)
	createGroups(t, s, group("a", ""), group("b", "a"), group("c", ""), group("d", ""))
	createRole(t, s, newRole("shop", "support", permGroupsRead))
	joinGroups(t, s, "shop", "u001", "b")
	if _, err := assign(s, "u001", "", "support"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := assign(s, "", "a", "support"); err != nil {
		t.Fatalf("assign group: %v", err)
	}
	if err := s.DeleteGroup(context.Background(), "shop", "d", audit("group_delete")); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	err := s.RunAdmin(context.Background(), store.AdminTxOptions{ReadOnly: true}, func(a store.AdminTx) error {
		ctx := context.Background()
		var sizes []int
		var cursors []string
		after := ""
		for {
			page, next, err := a.Accounts(ctx, "shop", after, 100)
			if err != nil {
				return err
			}
			if after == "" {
				first := page[0]
				if first.ID != "u001" || first.Username != "user1" || first.State != store.StateActive || !first.CreatedAt.Equal(t0) {
					t.Errorf("first account = %+v", first)
				}
				wantStrings(t, "groups of u001", first.GroupIDs, "b")
				wantStrings(t, "roles of u001", first.RoleIDs, "support")
				wantStrings(t, "roles of u002", page[1].RoleIDs)
			}
			sizes = append(sizes, len(page))
			cursors = append(cursors, next)
			if next == "" {
				break
			}
			after = next
		}
		if len(sizes) != 3 || sizes[0] != 100 || sizes[1] != 100 || sizes[2] != 5 {
			t.Errorf("page sizes = %v; want 100, 100, 5", sizes)
		}
		wantStrings(t, "cursors", cursors, "u100", "u200", "")

		groups, next, err := a.Groups(ctx, "shop", "", 2)
		if err != nil || len(groups) != 2 || groups[0].ID != "a" || groups[1].ID != "b" || next != "b" || groups[1].ParentID != "a" {
			t.Errorf("first group page = %+v, %q, %v", groups, next, err)
		}
		wantStrings(t, "roles of group a", groups[0].RoleIDs, "support")
		groups, next, err = a.Groups(ctx, "shop", "b", 2)
		if err != nil || len(groups) != 1 || groups[0].ID != "c" || next != "" {
			t.Errorf("second group page = %+v, %q, %v; want only c", groups, next, err)
		}

		roles, next, err := a.Roles(ctx, "shop", "", 1)
		if err != nil || len(roles) != 1 || roles[0].ID != "support" || next != "support" {
			t.Errorf("first role page = %+v, %q, %v", roles, next, err)
		}
		roles, next, err = a.Roles(ctx, "shop", next, 1)
		if err != nil || len(roles) != 1 || roles[0].ID != "tenant-admin" || !roles[0].Builtin || next != "" {
			t.Errorf("second role page = %+v, %q, %v", roles, next, err)
		}

		view, err := a.Account(ctx, "shop", "u205")
		if err != nil || view.Username != "user205" {
			t.Errorf("Account u205 = %+v, %v", view, err)
		}
		_, err = a.Account(ctx, "shop", "u206")
		wantErr(t, err, apperr.ErrNotFound, "account outside the tenant range")
		_, _, err = a.Accounts(ctx, "shop", "", 0)
		wantErr(t, err, apperr.ErrInvalidArgument, "zero limit")
		return nil
	})
	if err != nil {
		t.Fatalf("RunAdmin: %v", err)
	}
}
