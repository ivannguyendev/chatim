package authz_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
)

const (
	rolesRead    authz.Permission = "iam:roles.read"
	rolesWrite   authz.Permission = "iam:roles.write"
	rolesAssign  authz.Permission = "iam:roles.assign"
	groupsRead   authz.Permission = "iam:groups.read"
	groupsWrite  authz.Permission = "iam:groups.write"
	membersWrite authz.Permission = "iam:groups.members.write"
	accountsRead authz.Permission = "iam:accounts.read"
)

func grantOf(perms ...authz.Permission) authz.Grant {
	return authz.NewGrant(false, perms)
}

func tenantAdmin() authz.Grant {
	return authz.NewGrant(true, nil)
}

func TestHas(t *testing.T) {
	g := grantOf(groupsRead)
	if !g.Has(groupsRead) || g.Has(groupsWrite) {
		t.Fatalf("Has wrong for %v", g)
	}
	if !tenantAdmin().Has("chatim:rooms.read") {
		t.Fatal("tenant-admin must have every permission")
	}
	if (authz.Grant{}).Has(groupsRead) {
		t.Fatal("zero grant has nothing")
	}
}

func TestTenantAdminFlag(t *testing.T) {
	if !tenantAdmin().TenantAdmin() || grantOf(groupsRead).TenantAdmin() {
		t.Fatal("TenantAdmin flag wrong")
	}
}

func TestOnlyTenantAdminCoversTenantAdmin(t *testing.T) {
	all := grantOf(rolesRead, rolesWrite, rolesAssign, groupsRead, groupsWrite, membersWrite, accountsRead)
	if all.Covers(tenantAdmin()) {
		t.Fatal("a plain grant must not cover tenant-admin")
	}
	if !tenantAdmin().Covers(tenantAdmin()) {
		t.Fatal("tenant-admin must cover tenant-admin")
	}
}

func TestTenantAdminCoversEverything(t *testing.T) {
	ta := tenantAdmin()
	if !ta.Covers(grantOf(rolesWrite, "chatim:rooms.read")) || !ta.Covers(authz.Grant{}) {
		t.Fatal("tenant-admin must cover any grant")
	}
	if !ta.CoversAll([]authz.Permission{rolesWrite, "chatim:rooms.read"}) {
		t.Fatal("tenant-admin must cover any permission list")
	}
}

func TestCoversIsSubset(t *testing.T) {
	caller := grantOf(groupsRead, groupsWrite, rolesAssign)
	cases := []struct {
		other authz.Grant
		want  bool
	}{
		{authz.Grant{}, true},
		{grantOf(), true},
		{grantOf(groupsRead), true},
		{grantOf(groupsRead, groupsWrite, rolesAssign), true},
		{grantOf(groupsRead, rolesWrite), false},
		{grantOf(rolesWrite), false},
	}
	for _, c := range cases {
		if got := caller.Covers(c.other); got != c.want {
			t.Fatalf("Covers(%v) = %v, want %v", c.other, got, c.want)
		}
	}
	if !caller.CoversAll(nil) || !caller.CoversAll([]authz.Permission{groupsRead, rolesAssign}) {
		t.Fatal("CoversAll must accept a subset")
	}
	if caller.CoversAll([]authz.Permission{groupsRead, rolesWrite}) {
		t.Fatal("CoversAll must refuse a permission the caller lacks")
	}
	if (authz.Grant{}).Covers(grantOf(groupsRead)) {
		t.Fatal("zero grant covers only empty grants")
	}
}

func TestNewGrantDoesNotAliasInput(t *testing.T) {
	perms := []authz.Permission{groupsRead}
	g := authz.NewGrant(false, perms)
	perms[0] = rolesWrite
	if !g.Has(groupsRead) || g.Has(rolesWrite) {
		t.Fatal("grant must copy its input")
	}
}
