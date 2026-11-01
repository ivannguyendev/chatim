package admincmd_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestRoleCommandUsage(t *testing.T) {
	env, out, _ := testEnv(t, func(context.Context) (admincmd.Store, error) { return closedStore{}, nil })
	cases := [][]string{
		{"role-create", "-tenant", "shop", "-id", "support", "-name", "Support"},
		{"role-create", "-tenant", "shop", "-id", "support", "-permissions", "iam:roles.read"},
		{"role-create", "-id", "support", "-name", "Support", "-permissions", "iam:roles.read"},
		{"role-update", "-tenant", "shop", "-id", "support"},
		{"role-update", "-tenant", "shop", "-name", "Support"},
		{"role-delete", "-tenant", "shop"},
		{"role-delete", "-id", "support"},
		{"role-list"},
		{"role-list", "-tenant", "shop", "extra"},
		{"account-roles", "-tenant", "shop", "-username", "lan"},
		{"account-roles", "-tenant", "shop", "-roles", "support"},
		{"group-roles", "-tenant", "shop", "-group", "sales"},
		{"group-roles", "-tenant", "shop", "-roles", "support"},
		{"client-permissions-set", "-client", "chatim"},
		{"client-permissions-set", "-file", "catalog.txt"},
		{"account-create", "-tenant", "shop", "-role", "support"},
	}
	for _, args := range cases {
		if code := admincmd.Run(context.Background(), env, args); code != 2 {
			t.Fatalf("Run(%v) = %d, want 2", args, code)
		}
	}
	if out.Len() != 0 {
		t.Fatalf("usage errors printed %q, want nothing", out)
	}
}

func TestRoleCommandsAreKnown(t *testing.T) {
	for _, name := range []string{"role-create", "role-update", "role-delete", "role-list", "account-roles", "group-roles", "client-permissions-set"} {
		if !admincmd.Is(name) {
			t.Fatalf("Is(%q) = false", name)
		}
	}
}

func TestSplitPermissionsSortsAndRefusesBadParts(t *testing.T) {
	got, err := admincmd.SplitPermissionsForTest(" iam:roles.write , iam:accounts.read")
	if err != nil || !slices.Equal(got, []string{"iam:accounts.read", "iam:roles.write"}) {
		t.Fatalf("split = %v, %v; want sorted permissions", got, err)
	}
	if got, err := admincmd.SplitPermissionsForTest(""); err != nil || got == nil || len(got) != 0 {
		t.Fatalf(`split("") = %v, %v; want an empty list`, got, err)
	}
	for _, raw := range []string{",", "iam:roles.read,", "roles.read", "iam:roles.read,iam:roles.read", "IAM:roles.read"} {
		if _, err := admincmd.SplitPermissionsForTest(raw); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("split(%q) err = %v; want invalid argument", raw, err)
		}
	}
}

func TestParseCatalogReadsOnePermissionPerLine(t *testing.T) {
	text := "chatim:rooms.read Read rooms\n\n  chatim:rooms.write\tWrite   any room  \r\n"
	got, err := admincmd.ParseCatalogForTest(text)
	want := []store.ClientPermission{
		{Permission: "chatim:rooms.read", Description: "Read rooms"},
		{Permission: "chatim:rooms.write", Description: "Write   any room"},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("parse = %+v, %v; want %+v", got, err, want)
	}
	if got, err := admincmd.ParseCatalogForTest("\n \n"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("parse(blank) = %v, %v; want an empty list", got, err)
	}
	for _, text := range []string{"chatim:rooms.read", "chatim:rooms.read Read\nrooms Bad", "Chatim:rooms.read Read rooms"} {
		if _, err := admincmd.ParseCatalogForTest(text); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("parse(%q) err = %v; want invalid argument", text, err)
		}
	}
}
