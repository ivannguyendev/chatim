package authz_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestRequiredPermissionsAreIAMCatalogEntries(t *testing.T) {
	for rpc, p := range authz.Required {
		if !strings.HasPrefix(string(p), authz.IAMClient+":") {
			t.Fatalf("%s needs %s outside the iam client", rpc, p)
		}
		if !slices.Contains(authz.IAMPermissions, string(p)) {
			t.Fatalf("%s needs %s outside the iam catalog", rpc, p)
		}
	}
}

func TestRequiredCoversEveryAdminRPC(t *testing.T) {
	want := map[string]authz.Permission{
		"CreateRole":          rolesWrite,
		"UpdateRole":          rolesWrite,
		"DeleteRole":          rolesWrite,
		"GetRole":             rolesRead,
		"ListRoles":           rolesRead,
		"AssignAccountRole":   rolesAssign,
		"UnassignAccountRole": rolesAssign,
		"AssignGroupRole":     rolesAssign,
		"UnassignGroupRole":   rolesAssign,
		"CreateGroup":         groupsWrite,
		"UpdateGroup":         groupsWrite,
		"DeleteGroup":         groupsWrite,
		"ListGroups":          groupsRead,
		"SetAccountGroups":    membersWrite,
		"GetAccount":          accountsRead,
		"ListAccounts":        accountsRead,
	}
	if len(authz.Required) != len(want) {
		t.Fatalf("Required has %d rpcs, want %d", len(authz.Required), len(want))
	}
	for rpc, p := range want {
		if authz.Required[rpc] != p {
			t.Fatalf("Required[%s] = %q, want %q", rpc, authz.Required[rpc], p)
		}
	}
}

func TestWriteDoesNotImplyRead(t *testing.T) {
	err := authz.Require(grantOf(groupsWrite), "ListGroups")
	if !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "missing iam:groups.read") {
		t.Fatalf("err = %v", err)
	}
}
