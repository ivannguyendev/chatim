package authz_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestParsePermissionAcceptsCatalogShapes(t *testing.T) {
	inputs := append(slices.Clone(authz.IAMPermissions), "chatim:rooms.read", "a-1:b.c_2.d")
	for _, s := range inputs {
		p, err := authz.Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q) = %v", s, err)
		}
		if string(p) != s {
			t.Fatalf("Parse(%q) = %q", s, p)
		}
	}
}

func TestParsePermissionRejectsBadShapes(t *testing.T) {
	inputs := []string{
		"",
		"iam",
		"iamroles.read",
		"iam:roles",
		"iam:Roles.read",
		"IAM:roles.read",
		"iam:roles.*",
		"*:roles.read",
		"iam:roles..read",
		"iam:.roles.read",
		"iam:roles.read.",
		":roles.read",
		"iam:roles.read ",
		"iam: roles.read",
		"iam:roles.re ad",
		"1am:roles.read",
		"-iam:roles.read",
		"iam:1roles.read",
		"iam:roles.read:x",
		"iam_x:roles.read",
		"iam:roles-x.read",
		strings.Repeat("a", 33) + ":roles.read",
		"iam:" + strings.Repeat("r", 120) + ".read",
	}
	for _, s := range inputs {
		if _, err := authz.Parse(s); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("Parse(%q) = %v, want invalid argument", s, err)
		}
	}
}

func TestParsePermissionLengthLimits(t *testing.T) {
	client := strings.Repeat("a", 32)
	if _, err := authz.Parse(client + ":roles.read"); err != nil {
		t.Fatalf("32 byte client: %v", err)
	}
	longest := "iam:" + strings.Repeat("r", 128-len("iam:")-len(".read")) + ".read"
	if len(longest) != 128 {
		t.Fatalf("setup: len %d", len(longest))
	}
	if _, err := authz.Parse(longest); err != nil {
		t.Fatalf("128 bytes: %v", err)
	}
}

func TestParseAllRefusesDuplicates(t *testing.T) {
	_, err := authz.ParseAll([]string{"iam:roles.read", "iam:groups.read", "iam:roles.read"})
	if !errors.Is(err, apperr.ErrInvalidArgument) || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseAllRefusesBadShape(t *testing.T) {
	if _, err := authz.ParseAll([]string{"iam:roles.read", "bad"}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseAllSorts(t *testing.T) {
	got, err := authz.ParseAll([]string{"iam:roles.read", "chatim:rooms.read", "iam:groups.read"})
	if err != nil {
		t.Fatal(err)
	}
	want := []authz.Permission{"chatim:rooms.read", "iam:groups.read", "iam:roles.read"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestIAMPermissionsAreTheCatalog(t *testing.T) {
	want := []string{
		"iam:roles.read",
		"iam:roles.write",
		"iam:roles.assign",
		"iam:groups.read",
		"iam:groups.write",
		"iam:groups.members.write",
		"iam:accounts.read",
		"iam:accounts.create",
		"iam:accounts.update",
		"iam:accounts.reset_password",
		"iam:accounts.sessions.revoke",
		"iam:machines.write",
	}
	if !slices.Equal(authz.IAMPermissions, want) {
		t.Fatalf("IAMPermissions = %v", authz.IAMPermissions)
	}
	if authz.IAMClient != "iam" || authz.TenantAdminRole != "tenant-admin" {
		t.Fatalf("IAMClient = %q, TenantAdminRole = %q", authz.IAMClient, authz.TenantAdminRole)
	}
}

func TestValidateRoleID(t *testing.T) {
	for _, id := range []string{"a", "tenant-admin", "it_admin-2", strings.Repeat("z", 64), authz.TenantAdminRole} {
		if err := authz.ValidateRoleID(id); err != nil {
			t.Fatalf("ValidateRoleID(%q) = %v", id, err)
		}
	}
	for _, id := range []string{"", "IT", "it admin", "it:admin", "it.admin", "é", strings.Repeat("z", 65)} {
		if err := authz.ValidateRoleID(id); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("ValidateRoleID(%q) = %v", id, err)
		}
	}
}
