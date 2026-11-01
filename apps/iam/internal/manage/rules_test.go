package manage_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type escalationCase struct {
	name    string
	who     string
	grant   store.Grant
	setup   func(*world)
	op      func(*manage.Service, auth.Caller) error
	allowed bool
}

func escalationCases() []escalationCase {
	ctx := context.Background()
	minh := perms("iam:groups.read", "iam:groups.write", "iam:groups.members.write", "iam:roles.assign", "iam:accounts.read", "iam:roles.read")
	binh := perms("iam:groups.members.write", "iam:groups.read")
	writer := perms("iam:roles.write", "iam:groups.read")
	ta := store.Grant{TenantAdmin: true}
	strong := func(w *world) { w.grants["ha"] = perms("iam:roles.write") }
	haIsAdmin := func(w *world) { w.grants["ha"] = ta }
	createRole := func(ps ...string) func(*manage.Service, auth.Caller) error {
		return func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateRole(ctx, c, manage.NewRole{ID: "x", Name: "X", Permissions: ps})
			return err
		}
	}
	updateRole := func(ps ...string) func(*manage.Service, auth.Caller) error {
		return func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateRole(ctx, c, "reader", store.RoleUpdate{Permissions: &ps})
			return err
		}
	}
	assign := func(account, roleID string) func(*manage.Service, auth.Caller) error {
		return func(s *manage.Service, c auth.Caller) error { return s.AssignAccountRole(ctx, c, account, roleID) }
	}
	unassign := func(account string) func(*manage.Service, auth.Caller) error {
		return func(s *manage.Service, c auth.Caller) error {
			return s.UnassignAccountRole(ctx, c, account, "it-admin")
		}
	}
	groupAssign := func(roleID string) func(*manage.Service, auth.Caller) error {
		return func(s *manage.Service, c auth.Caller) error { return s.AssignGroupRole(ctx, c, "it-hcm", roleID) }
	}
	groupUnassign := func(groupID, roleID string) func(*manage.Service, auth.Caller) error {
		return func(s *manage.Service, c auth.Caller) error { return s.UnassignGroupRole(ctx, c, groupID, roleID) }
	}
	setGroups := func(account string, ids ...string) func(*manage.Service, auth.Caller) error {
		return func(s *manage.Service, c auth.Caller) error { return s.SetAccountGroups(ctx, c, account, ids) }
	}
	move := func(id, parent string) func(*manage.Service, auth.Caller) error {
		return func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateGroup(ctx, c, id, store.GroupUpdate{ParentID: &parent})
			return err
		}
	}
	create := func(parent string) func(*manage.Service, auth.Caller) error {
		return func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateGroup(ctx, c, manage.NewGroup{ID: "new", Name: "New", ParentID: parent})
			return err
		}
	}
	remove := func(id string) func(*manage.Service, auth.Caller) error {
		return func(s *manage.Service, c auth.Caller) error { return s.DeleteGroup(ctx, c, id) }
	}
	return []escalationCase{
		{"create role with held permissions", "lan", writer, nil, createRole("iam:roles.write"), true},
		{"create role with a missing permission", "lan", writer, nil, createRole("iam:accounts.read"), false},
		{"update role adding a held permission", "lan", writer, nil, updateRole("iam:groups.read", "iam:roles.write"), true},
		{"update role adding a missing permission", "lan", writer, nil, updateRole("iam:accounts.read"), false},
		{"update role dropping a missing permission", "lan", perms("iam:roles.write"), nil, updateRole(), true},

		{"assign covered role to covered account", "minh", minh, nil, assign("ha", "reader"), true},
		{"assign a stronger role", "minh", minh, nil, assign("ha", "hr-admin"), false},
		{"assign to a stronger account", "minh", minh, strong, assign("ha", "reader"), false},
		{"assign tenant-admin without being one", "minh", minh, nil, assign("ha", authz.TenantAdminRole), false},
		{"assign to a tenant-admin account", "minh", minh, haIsAdmin, assign("ha", "reader"), false},
		{"tenant-admin assigns tenant-admin", "lan", ta, nil, assign("ha", authz.TenantAdminRole), true},
		{"unassign from covered account", "minh", minh, nil, unassign("ha"), true},
		{"unassign from a stronger account", "minh", minh, strong, unassign("ha"), false},
		{"unassign from a tenant-admin account", "minh", minh, haIsAdmin, unassign("ha"), false},

		{"group role assign of a covered role", "minh", minh, nil, groupAssign("it-admin"), true},
		{"group role assign of a stronger role", "minh", minh, nil, groupAssign("hr-admin"), false},
		{"group role assign of tenant-admin", "minh", minh, nil, groupAssign(authz.TenantAdminRole), false},
		{"group role unassign of a plain role", "minh", minh, nil, groupUnassign("hr", "hr-admin"), true},
		{"group role unassign of tenant-admin", "minh", minh, nil, groupUnassign("admins", authz.TenantAdminRole), false},
		{"tenant-admin unassigns tenant-admin from a group", "lan", ta, nil, groupUnassign("admins", authz.TenantAdminRole), true},

		{"set groups of a covered account to covered groups", "binh", binh, nil, setGroups("ha", "cong-ty"), true},
		{"binh joins admins", "binh", binh, nil, setGroups("binh", "admins"), false},
		{"set groups granting more through an ancestor", "binh", binh, nil, setGroups("ha", "it-hcm"), false},
		{"set groups of a stronger account", "binh", binh, strong, setGroups("ha"), false},
		{"set groups of a tenant-admin account", "binh", binh, haIsAdmin, setGroups("ha"), false},

		{"move under a covered chain", "minh", minh, nil, move("it-hcm", "cong-ty"), true},
		{"minh moves it-hcm under hr", "minh", minh, nil, move("it-hcm", "hr"), false},
		{"move under admins", "minh", minh, nil, move("it-hcm", "admins"), false},
		{"move out of an ancestor holding tenant-admin", "minh", minh, nil, move("ops", ""), false},
		{"tenant-admin moves out of a tenant-admin chain", "lan", ta, nil, move("ops", "hr"), true},
		{"delete group under an ancestor holding tenant-admin", "minh", minh, nil, remove("ops"), false},
		{"delete group holding a stronger plain role", "minh", minh, nil, remove("hr"), true},
		{"tenant-admin deletes under a tenant-admin chain", "lan", ta, nil, remove("ops"), true},

		{"create root group", "minh", minh, nil, create(""), true},
		{"create under a covered parent", "minh", minh, nil, create("it-hcm"), true},
		{"create under a stronger parent", "minh", minh, nil, create("hr"), false},
		{"create under admins", "minh", minh, nil, create("admins"), false},
		{"tenant-admin creates under admins", "lan", ta, nil, create("admins"), true},
	}
}

func TestEscalationPerRPC(t *testing.T) {
	for _, tc := range escalationCases() {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld()
			w.grants[tc.who] = tc.grant
			if tc.setup != nil {
				tc.setup(w)
			}
			svc, r := newService(w)
			err := tc.op(svc, caller(tc.who))
			if tc.allowed {
				if err != nil || len(r.audits) != 1 {
					t.Fatalf("want allowed, got %v, audits %v", err, r.audits)
				}
				return
			}
			if !errors.Is(err, apperr.ErrPermissionDenied) || strings.Contains(err.Error(), "missing") {
				t.Fatalf("want an escalation denial, got %v", err)
			}
			if len(r.audits) != 0 {
				t.Fatalf("audits = %v", r.audits)
			}
		})
	}
}

func TestEscalationRefusalsRollBackWithoutAudit(t *testing.T) {
	for _, tc := range escalationCases() {
		if tc.allowed {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld()
			w.grants[tc.who] = tc.grant
			if tc.setup != nil {
				tc.setup(w)
			}
			svc, r := newService(w)
			_ = tc.op(svc, caller(tc.who))
			wrote := r.wrote()
			checkedAfterWrite := []string{"CreateRole", "UpdateRole", "SetAccountGroups"}
			if len(wrote) > 1 || (len(wrote) == 1 && !slices.Contains(checkedAfterWrite, wrote[0])) {
				t.Fatalf("wrote %v", wrote)
			}
			if r.rollbacks != 1 || len(r.audits) != 0 {
				t.Fatalf("rollbacks = %d, audits = %v", r.rollbacks, r.audits)
			}
		})
	}
}

func TestGetAccountHidesGroupsAndRolesWithoutReadPermissions(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		grant              store.Grant
		wantGroup, wantRol bool
	}{
		{perms("iam:accounts.read"), false, false},
		{perms("iam:accounts.read", "iam:groups.read"), true, false},
		{perms("iam:accounts.read", "iam:roles.read"), false, true},
		{store.Grant{TenantAdmin: true}, true, true},
	}
	for _, tc := range cases {
		w := newWorld()
		w.grants["lan"] = tc.grant
		svc, _ := newService(w)
		got, err := svc.GetAccount(ctx, caller("lan"), "ha")
		if err != nil {
			t.Fatal(err)
		}
		page, _, err := svc.ListAccounts(ctx, caller("lan"), "")
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range append(page, got) {
			if a.GroupIDs == nil || a.RoleIDs == nil {
				t.Fatalf("hidden lists must be empty, not nil: %+v", a)
			}
			if (len(a.GroupIDs) > 0) != tc.wantGroup || (len(a.RoleIDs) > 0) != tc.wantRol {
				t.Fatalf("grant %+v: account %+v", tc.grant, a)
			}
		}
		if w.accounts["ha"].GroupIDs[0] != "it" || w.accounts["ha"].RoleIDs[0] != "reader" {
			t.Fatal("masking must not change the stored view")
		}
	}
}

func TestListsPageBy100FromTheToken(t *testing.T) {
	ctx := context.Background()
	svc, r := newService(newWorld())
	c := caller("minh")
	_, next, err := svc.ListAccounts(ctx, c, "ha")
	if err != nil || next != "lan" {
		t.Fatalf("next = %q, err = %v", next, err)
	}
	if _, next, err = svc.ListRoles(ctx, c, "reader"); err != nil || next != "" {
		t.Fatalf("next = %q, err = %v", next, err)
	}
	if _, next, err = svc.ListGroups(ctx, c, "cong-ty"); err != nil || next != "it" {
		t.Fatalf("next = %q, err = %v", next, err)
	}
	want := []string{"accounts ha 100", "roles reader 100", "groups cong-ty 100"}
	if !slices.Equal(r.pages, want) || manage.PageSize != 100 {
		t.Fatalf("pages = %v", r.pages)
	}
}

func TestInvalidInputNeverOpensATransaction(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("x", 65)
	cases := map[string]func(*manage.Service, auth.Caller) error{
		"role id": func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateRole(ctx, c, manage.NewRole{ID: "IT", Name: "IT"})
			return err
		},
		"role name": func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateRole(ctx, c, manage.NewRole{ID: "it", Name: " \u202e "})
			return err
		},
		"permission shape": func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateRole(ctx, c, manage.NewRole{ID: "it", Name: "IT", Permissions: []string{"iam:roles"}})
			return err
		},
		"permission twice": func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateRole(ctx, c, manage.NewRole{ID: "it", Name: "IT", Permissions: []string{"iam:roles.read", "iam:roles.read"}})
			return err
		},
		"update role nothing": func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateRole(ctx, c, "it", store.RoleUpdate{})
			return err
		},
		"update role bad name": func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateRole(ctx, c, "it", store.RoleUpdate{Name: new("")})
			return err
		},
		"update role bad permission": func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateRole(ctx, c, "it", store.RoleUpdate{Permissions: &[]string{"x"}})
			return err
		},
		"delete role id": func(s *manage.Service, c auth.Caller) error {
			_, err := s.DeleteRole(ctx, c, "")
			return err
		},
		"get role id": func(s *manage.Service, c auth.Caller) error {
			_, err := s.GetRole(ctx, c, long)
			return err
		},
		"role page token": func(s *manage.Service, c auth.Caller) error {
			_, _, err := s.ListRoles(ctx, c, "Bad Token")
			return err
		},
		"assign account id": func(s *manage.Service, c auth.Caller) error { return s.AssignAccountRole(ctx, c, "a b", "reader") },
		"assign role id":    func(s *manage.Service, c auth.Caller) error { return s.AssignAccountRole(ctx, c, "ha", "") },
		"unassign account":  func(s *manage.Service, c auth.Caller) error { return s.UnassignAccountRole(ctx, c, "", "reader") },
		"group assign id":   func(s *manage.Service, c auth.Caller) error { return s.AssignGroupRole(ctx, c, "a b", "reader") },
		"group unassign":    func(s *manage.Service, c auth.Caller) error { return s.UnassignGroupRole(ctx, c, "it", "R") },
		"group id": func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateGroup(ctx, c, manage.NewGroup{ID: "a b", Name: "A"})
			return err
		},
		"group name": func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateGroup(ctx, c, manage.NewGroup{ID: "a", Name: ""})
			return err
		},
		"group parent": func(s *manage.Service, c auth.Caller) error {
			_, err := s.CreateGroup(ctx, c, manage.NewGroup{ID: "a", Name: "A", ParentID: "a b"})
			return err
		},
		"update group nothing": func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateGroup(ctx, c, "it", store.GroupUpdate{})
			return err
		},
		"update group parent": func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateGroup(ctx, c, "it", store.GroupUpdate{ParentID: new("a b")})
			return err
		},
		"update group name": func(s *manage.Service, c auth.Caller) error {
			_, err := s.UpdateGroup(ctx, c, "it", store.GroupUpdate{Name: new("\x00")})
			return err
		},
		"delete group id": func(s *manage.Service, c auth.Caller) error { return s.DeleteGroup(ctx, c, long) },
		"set groups twice": func(s *manage.Service, c auth.Caller) error {
			return s.SetAccountGroups(ctx, c, "ha", []string{"it", "it"})
		},
		"set groups shape": func(s *manage.Service, c auth.Caller) error { return s.SetAccountGroups(ctx, c, "ha", []string{"a b"}) },
		"set groups account": func(s *manage.Service, c auth.Caller) error {
			return s.SetAccountGroups(ctx, c, long, nil)
		},
		"group page token": func(s *manage.Service, c auth.Caller) error {
			_, _, err := s.ListGroups(ctx, c, "a b")
			return err
		},
		"get account id": func(s *manage.Service, c auth.Caller) error {
			_, err := s.GetAccount(ctx, c, "a/b")
			return err
		},
		"account page token": func(s *manage.Service, c auth.Caller) error {
			_, _, err := s.ListAccounts(ctx, c, "a/b")
			return err
		},
	}
	for name, op := range cases {
		t.Run(name, func(t *testing.T) {
			svc, r := newService(newWorld())
			if err := op(svc, caller("minh")); !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("err = %v, want invalid argument", err)
			}
			if len(r.runs) != 0 {
				t.Fatalf("opened %d transactions", len(r.runs))
			}
		})
	}
}
