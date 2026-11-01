package admincmd_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

var (
	e2eAccountLine  = regexp.MustCompile(`(?m)^account ([^ ]*) created for .*$`)
	e2ePasswordLine = regexp.MustCompile(`(?m)^temporary password: (.+)$`)
)

func countRows(t *testing.T, raw *pgx.Conn, query string, args ...any) int {
	t.Helper()
	var n int
	if err := raw.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func wantRoleList(t *testing.T, h *harness, rows ...string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(h.run("role-list", "-tenant", "shop"), "\n"), "\n")
	got := make([]string, len(lines))
	for i, line := range lines {
		got[i] = strings.Join(strings.Fields(line), " ")
	}
	want := append([]string{"ROLE NAME BUILTIN PERMISSIONS"}, rows...)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("role-list = %q; want %q", got, want)
	}
}

func tenantAdmin(t *testing.T, h *harness, accountID string) bool {
	t.Helper()
	var g store.Grant
	err := h.store().RunAdmin(context.Background(), store.AdminTxOptions{Tenant: "shop", ReadOnly: true}, func(tx store.AdminTx) error {
		var err error
		g, err = tx.AccountGrant(context.Background(), "shop", accountID)
		return err
	})
	if err != nil {
		t.Fatalf("AccountGrant: %v", err)
	}
	return g.TenantAdmin
}

func TestRoleCommands(t *testing.T) {
	h, raw := newShop(t)
	out := h.run("role-create", "-tenant", "shop", "-id", "support", "-name", "  Support  ", "-permissions", "iam:groups.read, iam:accounts.read")
	if out != "role support created\n" {
		t.Fatalf("role-create output = %q", out)
	}
	wantRoleList(t, h, "support Support no iam:accounts.read, iam:groups.read", "tenant-admin Tenant admin yes all")
	if logs := h.fail(1, "role-create", "-tenant", "shop", "-id", "typo", "-name", "Typo", "-permissions", "iam:acounts.read"); !strings.Contains(logs, "permission not in catalog: iam:acounts.read") {
		t.Fatalf("unknown permission logs = %s", logs)
	}
	if out := h.run("role-update", "-tenant", "shop", "-id", "support", "-name", "Help desk"); out != "role support updated\n" {
		t.Fatalf("role-update output = %q", out)
	}
	h.run("role-update", "-tenant", "shop", "-id", "support", "-permissions", "iam:accounts.read")
	wantRoleList(t, h, "support Help desk no iam:accounts.read", "tenant-admin Tenant admin yes all")
	h.fail(1, "role-update", "-tenant", "shop", "-id", "tenant-admin", "-name", "Boss")
	h.fail(1, "role-delete", "-tenant", "shop", "-id", "tenant-admin")

	lan := e2eAccountLine.FindStringSubmatch(h.run("account-create", "-tenant", "shop", "-username", "lan"))[1]
	h.run("group-create", "-tenant", "shop", "-id", "sales", "-name", "Sales")
	if out := h.run("account-roles", "-tenant", "shop", "-username", "Lan", "-roles", "support, tenant-admin"); out != "account lan roles: support, tenant-admin\n" {
		t.Fatalf("account-roles output = %q", out)
	}
	if out := h.run("group-roles", "-tenant", "shop", "-group", "sales", "-roles", "support"); out != "group sales roles: support\n" {
		t.Fatalf("group-roles output = %q", out)
	}
	h.fail(1, "account-roles", "-tenant", "shop", "-username", "lan", "-roles", "nope")
	h.fail(1, "group-roles", "-tenant", "shop", "-group", "absent", "-roles", "support")

	out = h.run("role-delete", "-tenant", "shop", "-id", "support")
	if out != "role support deleted; unassigned from 1 accounts and 1 groups\n" {
		t.Fatalf("role-delete output = %q", out)
	}
	if n := countRows(t, raw, "SELECT (SELECT count(*) FROM account_roles WHERE role_id = 'support') + (SELECT count(*) FROM group_roles WHERE role_id = 'support')"); n != 0 {
		t.Fatalf("bindings left after role-delete = %d, want 0", n)
	}
	if n := countRows(t, raw, "SELECT count(*) FROM account_roles WHERE tenant_id = 'shop' AND account_id = $1 AND role_id = 'tenant-admin'", lan); n != 1 {
		t.Fatalf("tenant-admin bindings of lan = %d, want 1", n)
	}
	h.fail(1, "role-delete", "-tenant", "shop", "-id", "support")
	wantRoleList(t, h, "tenant-admin Tenant admin yes all")

	for action, want := range map[string]int{"role_create": 1, "role_update": 2, "role_delete": 1, "account_roles_set": 1, "group_roles_set": 1} {
		wantCLIAudits(t, raw, action, want)
	}
	detail := countRows(t, raw, "SELECT count(*) FROM audit_events WHERE action = 'role_delete' AND detail = '{\"role\": \"support\", \"accounts\": \"1\", \"groups\": \"1\"}'::jsonb")
	if detail != 1 {
		t.Fatal("role_delete audit lacks the role and the unassigned counts")
	}
}

func TestAccountCreateWithRoleKeepsTheOutputLines(t *testing.T) {
	h, raw := newShop(t)
	plain := h.run("account-create", "-tenant", "shop", "-username", "binh")
	if lines := strings.Split(strings.TrimRight(plain, "\n"), "\n"); len(lines) != 2 {
		t.Fatalf("account-create without -role printed %q; want the two old lines only", plain)
	}
	out := h.run("account-create", "-tenant", "shop", "-username", "Lan", "-name", "Lan", "-role", "tenant-admin")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	account := e2eAccountLine.FindStringSubmatch(out)
	if len(lines) != 3 || account == nil || !strings.HasSuffix(lines[0], " created for lan") ||
		!e2ePasswordLine.MatchString(lines[1]) || lines[2] != "role: tenant-admin" {
		t.Fatalf("account-create -role output = %q", out)
	}
	if !tenantAdmin(t, h, account[1]) {
		t.Fatal("account created with -role tenant-admin is not a tenant admin")
	}
	wantCLIAudits(t, raw, "account_create", 2)
	wantCLIAudits(t, raw, "account_role_assign", 1)

	h.fail(1, "account-create", "-tenant", "shop", "-username", "minh", "-role", "nope")
	h.fail(1, "account-create", "-tenant", "shop", "-username", "minh", "-role", "Bad Role")
	if _, err := h.store().AccountByUsername(context.Background(), "shop", "minh"); err == nil {
		t.Fatal("account-create with an unknown role created the account")
	}
	if n := countRows(t, raw, "SELECT count(*) FROM accounts WHERE tenant_id = 'shop'"); n != 2 {
		t.Fatalf("accounts = %d, want 2", n)
	}
	wantCLIAudits(t, raw, "account_create", 2)
}

func TestRescueALostTenantAdmin(t *testing.T) {
	h, raw := newShop(t)
	lan := e2eAccountLine.FindStringSubmatch(h.run("account-create", "-tenant", "shop", "-username", "lan", "-role", "tenant-admin"))[1]
	if out := h.run("account-roles", "-tenant", "shop", "-username", "lan", "-roles", ""); out != "account lan roles: none\n" {
		t.Fatalf("account-roles none output = %q", out)
	}
	if n := countRows(t, raw, "SELECT count(*) FROM account_roles WHERE tenant_id = 'shop' AND role_id = 'tenant-admin'"); n != 0 || tenantAdmin(t, h, lan) {
		t.Fatalf("tenant-admin bindings = %d after removing every role; want 0 and no tenant admin", n)
	}
	if out := h.run("account-roles", "-tenant", "shop", "-username", "lan", "-roles", "tenant-admin"); out != "account lan roles: tenant-admin\n" {
		t.Fatalf("account-roles rescue output = %q", out)
	}
	if !tenantAdmin(t, h, lan) {
		t.Fatal("account-roles -roles tenant-admin did not restore the tenant admin")
	}
	wantCLIAudits(t, raw, "account_roles_set", 2)
}

func TestClientPermissionsSet(t *testing.T) {
	h, raw := newShop(t)
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	catalog := func() string {
		rows, err := raw.Query(context.Background(), "SELECT permission || '=' || description FROM client_permissions WHERE client_id = 'chatim' ORDER BY permission")
		if err != nil {
			t.Fatal(err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(got, "; ")
	}

	first := write("first.txt", "chatim:rooms.read Read rooms\n\nchatim:rooms.write   Write any room\n")
	if out := h.run("client-permissions-set", "-client", "chatim", "-file", first); out != "client chatim: 2 permissions\n" {
		t.Fatalf("client-permissions-set output = %q", out)
	}
	if got := catalog(); got != "chatim:rooms.read=Read rooms; chatim:rooms.write=Write any room" {
		t.Fatalf("catalog = %q", got)
	}
	h.run("role-create", "-tenant", "shop", "-id", "reader", "-name", "Reader", "-permissions", "chatim:rooms.read")

	dropRead := write("drop-read.txt", "chatim:rooms.write Write any room\n")
	if logs := h.fail(1, "client-permissions-set", "-client", "chatim", "-file", dropRead); !strings.Contains(logs, "permission in use: chatim:rooms.read") {
		t.Fatalf("in-use logs = %s", logs)
	}
	replace := write("replace.txt", "chatim:rooms.read Read any room\nchatim:members.read Read members\n")
	if out := h.run("client-permissions-set", "-client", "chatim", "-file", replace); out != "client chatim: 2 permissions\n" {
		t.Fatalf("replace output = %q", out)
	}
	if got := catalog(); got != "chatim:members.read=Read members; chatim:rooms.read=Read any room" {
		t.Fatalf("catalog after replace = %q", got)
	}

	iam := write("iam.txt", "iam:roles.read Read roles\n")
	if logs := h.fail(1, "client-permissions-set", "-client", "iam", "-file", iam); !strings.Contains(logs, "the iam catalog comes from migrations") {
		t.Fatalf("iam client logs = %s", logs)
	}
	h.fail(1, "client-permissions-set", "-client", "chatim", "-file", write("bad.txt", "chatim:rooms.read\n"))
	h.fail(1, "client-permissions-set", "-client", "chatim", "-file", filepath.Join(dir, "absent.txt"))
	if n := countRows(t, raw, "SELECT count(*) FROM client_permissions WHERE client_id = 'iam'"); n != 12 {
		t.Fatalf("iam catalog size = %d, want 12", n)
	}
	audits := countRows(t, raw, "SELECT count(*) FROM audit_events WHERE action = 'client_permissions_set' AND actor = 'admin-cli' AND tenant_id = '' AND detail->>'client' = 'chatim'")
	if audits != 2 || countRows(t, raw, "SELECT count(*) FROM audit_events WHERE action = 'client_permissions_set'") != 2 {
		t.Fatalf("client_permissions_set audits by admin-cli = %d, want 2 and no other", audits)
	}
}
