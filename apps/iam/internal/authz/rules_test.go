package authz_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type escalationCase struct {
	name    string
	check   func() error
	allowed bool
}

func escalationCases() []escalationCase {
	ta := tenantAdmin()
	itAdmin := grantOf(groupsRead, groupsWrite, membersWrite, rolesAssign, accountsRead)
	hrAdmin := grantOf(rolesWrite)
	minh := itAdmin
	binh := grantOf(membersWrite, groupsRead)
	ha := grantOf(groupsRead)
	readers := grantOf(groupsRead)
	none := authz.Grant{}
	return []escalationCase{
		{"require granted", func() error { return authz.Require(minh, "ListGroups") }, true},
		{"require missing", func() error { return authz.Require(minh, "CreateRole") }, false},
		{"require unknown rpc", func() error { return authz.Require(ta, "DropTenant") }, false},

		{"role write adds held permission", func() error { return authz.CheckRoleWrite(minh, []authz.Permission{groupsRead}) }, true},
		{"role write adds nothing", func() error { return authz.CheckRoleWrite(binh, nil) }, true},
		{"role write adds missing permission", func() error { return authz.CheckRoleWrite(minh, []authz.Permission{rolesWrite}) }, false},
		{"tenant-admin role write adds any", func() error { return authz.CheckRoleWrite(ta, []authz.Permission{rolesWrite, "chatim:rooms.read"}) }, true},

		{"assign covered role to covered account", func() error { return authz.CheckAccountRoleAssign(minh, itAdmin, ha) }, true},
		{"assign stronger role", func() error { return authz.CheckAccountRoleAssign(minh, hrAdmin, ha) }, false},
		{"assign to stronger account", func() error { return authz.CheckAccountRoleAssign(minh, readers, hrAdmin) }, false},
		{"assign to tenant-admin account", func() error { return authz.CheckAccountRoleAssign(minh, readers, ta) }, false},
		{"assign tenant-admin role without being one", func() error { return authz.CheckAccountRoleAssign(minh, ta, ha) }, false},
		{"tenant-admin assigns tenant-admin role", func() error { return authz.CheckAccountRoleAssign(ta, ta, ha) }, true},

		{"unassign from covered account", func() error { return authz.CheckAccountRoleUnassign(minh, ha) }, true},
		{"unassign from stronger account", func() error { return authz.CheckAccountRoleUnassign(minh, hrAdmin) }, false},
		{"unassign from tenant-admin account", func() error { return authz.CheckAccountRoleUnassign(minh, ta) }, false},
		{"tenant-admin unassigns from tenant-admin", func() error { return authz.CheckAccountRoleUnassign(ta, ta) }, true},

		{"group role assign checks no member", func() error { return authz.CheckGroupRoleAssign(minh, itAdmin) }, true},
		{"group role assign stronger role", func() error { return authz.CheckGroupRoleAssign(minh, hrAdmin) }, false},
		{"group role assign tenant-admin without being one", func() error { return authz.CheckGroupRoleAssign(minh, ta) }, false},

		{"group role unassign plain role", func() error { return authz.CheckGroupRoleUnassign(binh, false) }, true},
		{"group role unassign tenant-admin without being one", func() error { return authz.CheckGroupRoleUnassign(minh, true) }, false},
		{"tenant-admin unassigns tenant-admin from group", func() error { return authz.CheckGroupRoleUnassign(ta, true) }, true},

		{"set groups of covered account to covered groups", func() error { return authz.CheckAccountGroups(binh, ha, readers) }, true},
		{"binh puts himself into admins", func() error { return authz.CheckAccountGroups(binh, binh, ta) }, false},
		{"set groups granting a missing permission", func() error { return authz.CheckAccountGroups(binh, ha, hrAdmin) }, false},
		{"set groups of stronger account", func() error { return authz.CheckAccountGroups(binh, hrAdmin, none) }, false},
		{"set groups of tenant-admin account", func() error { return authz.CheckAccountGroups(binh, ta, none) }, false},

		{"move under covered chain", func() error { return authz.CheckGroupMove(minh, readers, itAdmin) }, true},
		{"minh moves it-hcm under hr", func() error { return authz.CheckGroupMove(minh, itAdmin, hrAdmin) }, false},
		{"move group under tenant-admin chain", func() error { return authz.CheckGroupMove(minh, none, ta) }, false},
		{"move group out of tenant-admin chain", func() error { return authz.CheckGroupMove(minh, ta, none) }, false},
		{"tenant-admin moves tenant-admin group", func() error { return authz.CheckGroupMove(ta, ta, hrAdmin) }, true},

		{"delete group whose chain holds a stronger plain role", func() error { return authz.CheckGroupDelete(minh, hrAdmin) }, true},
		{"delete group in tenant-admin chain", func() error { return authz.CheckGroupDelete(minh, ta) }, false},
		{"tenant-admin deletes tenant-admin group", func() error { return authz.CheckGroupDelete(ta, ta) }, true},

		{"create group under covered parent", func() error { return authz.CheckGroupCreate(minh, itAdmin) }, true},
		{"create root group", func() error { return authz.CheckGroupCreate(binh, none) }, true},
		{"create group under hr", func() error { return authz.CheckGroupCreate(minh, hrAdmin) }, false},
		{"create group under admins", func() error { return authz.CheckGroupCreate(minh, ta) }, false},
		{"tenant-admin creates group under admins", func() error { return authz.CheckGroupCreate(ta, ta) }, true},
	}
}

func TestEscalationTable(t *testing.T) {
	for _, c := range escalationCases() {
		t.Run(c.name, func(t *testing.T) {
			err := c.check()
			if c.allowed && err != nil {
				t.Fatalf("want allowed, got %v", err)
			}
			if !c.allowed && !errors.Is(err, apperr.ErrPermissionDenied) {
				t.Fatalf("want permission denied, got %v", err)
			}
		})
	}
}
