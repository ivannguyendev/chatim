package pgstore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

const (
	planTenants        = 2000
	planShopAccounts   = 5000
	planShopTreeLevels = 10
	planAccount        = "u000043"
)

const recursiveGrantSQL = `WITH RECURSIVE up(id, parent) AS (
	SELECT g.id, g.parent_id FROM account_groups ag JOIN groups g ON g.tenant_id = ag.tenant_id AND g.id = ag.group_id
	WHERE ag.tenant_id = 'shop' AND ag.account_id = $1 AND g.state = 1
	UNION
	SELECT g.id, g.parent_id FROM up JOIN groups g ON g.tenant_id = 'shop' AND g.id = up.parent
), held AS (
	SELECT gr.role_id FROM up JOIN group_roles gr ON gr.tenant_id = 'shop' AND gr.group_id = up.id
	UNION
	SELECT role_id FROM account_roles WHERE tenant_id = 'shop' AND account_id = $1
)
SELECT DISTINCT rp.permission FROM held JOIN role_permissions rp ON rp.tenant_id = 'shop' AND rp.role_id = held.role_id ORDER BY 1`

var grantPlanSeed = []string{
	`INSERT INTO tenants (id, name, state, ver, created_at, updated_at, updated_by)
		SELECT 't' || lpad(n::text, 5, '0'), 'T' || n, 1, 1, now(), now(), 'test' FROM generate_series(1, $1::int) n`,
	`INSERT INTO roles (tenant_id, id, name, builtin, ver, created_at, updated_at, updated_by)
		SELECT t.id, 'role-' || n, 'Role ' || n, false, 1, now(), now(), 'test' FROM tenants t, generate_series(1, 5) n`,
	`INSERT INTO role_permissions (tenant_id, role_id, permission)
		SELECT r.tenant_id, r.id, p.permission FROM roles r
		CROSS JOIN LATERAL (SELECT permission FROM client_permissions ORDER BY md5(r.tenant_id || r.id || permission) LIMIT 3) p
		WHERE NOT r.builtin`,
	`INSERT INTO accounts (tenant_id, id, username, display_name, state, password_hash, password_must_change, password_changed_at, ver, created_at, updated_at, updated_by)
		SELECT t.id, 'u' || lpad(n::text, 6, '0'), 'user' || n, 'User ' || n, 1, 'x', false, now(), 1, now(), now(), 'test'
		FROM tenants t, generate_series(1, CASE WHEN t.id = 'shop' THEN $1::int ELSE 10 END) n`,
	`INSERT INTO groups (tenant_id, id, name, state, created_at, updated_at, updated_by)
		SELECT t.id, 'g' || lpad(n::text, 4, '0'), 'G' || n, 1, now(), now(), 'test'
		FROM tenants t, generate_series(1, 20) n WHERE t.id <> 'shop'`,
	`INSERT INTO group_roles (tenant_id, group_id, role_id, created_at, created_by)
		SELECT g.tenant_id, g.id, 'role-' || (right(g.id, 1)::int % 5 + 1), now(), 'test' FROM groups g WHERE right(g.id, 1)::int % 2 = 0`,
	`INSERT INTO account_roles (tenant_id, account_id, role_id, created_at, created_by)
		SELECT a.tenant_id, a.id, 'role-' || (right(a.id, 1)::int % 5 + 1), now(), 'test' FROM accounts a WHERE right(a.id, 1)::int % 3 = 0`,
}

const shopTreeLevelSQL = `INSERT INTO groups (tenant_id, id, name, parent_id, state, created_at, updated_at, updated_by)
	SELECT 'shop', 'g' || lpad(n::text, 4, '0'), 'G' || n, CASE WHEN n > 1 THEN 'g' || lpad((n / 2)::text, 4, '0') END, 1, now(), now(), 'test'
	FROM generate_series($1::int, $2::int) n`

const shopMembershipSQL = `INSERT INTO account_groups (tenant_id, account_id, group_id)
	SELECT 'shop', 'u' || lpad(n::text, 6, '0'), 'g' || lpad((512 + (n * k) % 512)::text, 4, '0')
	FROM generate_series(1, $1::int) n, (VALUES (1), (7)) v(k)
	ON CONFLICT DO NOTHING`

func seedGrantPlanData(t *testing.T, s fixture) {
	t.Helper()
	seedTenant(t, s, "shop")
	exec(t, s.raw, grantPlanSeed[0], planTenants)
	for _, sql := range grantPlanSeed[1:3] {
		exec(t, s.raw, sql)
	}
	exec(t, s.raw, grantPlanSeed[3], planShopAccounts)
	exec(t, s.raw, grantPlanSeed[4])
	for level := range planShopTreeLevels {
		exec(t, s.raw, shopTreeLevelSQL, 1<<level, 1<<(level+1)-1)
	}
	exec(t, s.raw, shopMembershipSQL, planShopAccounts)
	for _, sql := range grantPlanSeed[5:] {
		exec(t, s.raw, sql)
	}
	exec(t, s.raw, "INSERT INTO account_roles (tenant_id, account_id, role_id, created_at, created_by) VALUES ('shop', $1, 'tenant-admin', now(), 'test')", planAccount)
	exec(t, s.raw, "ANALYZE")
}

func explainGrant(t *testing.T, s fixture, sql string, args ...any) planNode {
	t.Helper()
	var raw []byte
	if err := s.raw.QueryRow(context.Background(), "EXPLAIN (ANALYZE, FORMAT JSON) "+sql, args...).Scan(&raw); err != nil {
		t.Fatalf("explain: %v", err)
	}
	var plans []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
		t.Fatalf("parse plan: %v (%d plans)", err, len(plans))
	}
	return plans[0].Plan
}

func tableScans(n planNode, out []string) []string {
	if n.RelationName != "" {
		out = append(out, fmt.Sprintf("%s %s using %s rows=%.0f loops=%.0f", n.NodeType, n.RelationName, n.IndexName, n.ActualRows, n.ActualLoops))
	}
	for _, child := range n.Plans {
		out = tableScans(child, out)
	}
	return out
}

func wantOnlyKeyScans(t *testing.T, name string, n planNode) {
	t.Helper()
	scans := tableScans(n, nil)
	t.Logf("%s plan:\n  %s", name, strings.Join(scans, "\n  "))
	for _, scan := range scans {
		if !strings.HasPrefix(scan, "Index Scan ") && !strings.HasPrefix(scan, "Index Only Scan ") || !strings.Contains(scan, "_pkey ") {
			t.Errorf("%s: %s; want only primary key index scans", name, scan)
		}
	}
}

func TestGrantQueriesReadOnlyByPrimaryKeys(t *testing.T) {
	s := openStore(t)
	seedGrantPlanData(t, s)
	want := queryStrings(t, s.raw, recursiveGrantSQL, planAccount)
	if len(want) == 0 {
		t.Fatal("the seeded account holds no permission")
	}
	wantGrant(t, planAccount, accountGrant(t, s, "shop", planAccount), true, want...)
	wantOnlyKeyScans(t, "AccountGrant", explainGrant(t, s, pgstore.AccountGrantSQL, "shop", planAccount, 1))
	wantOnlyKeyScans(t, "GroupsGrant", explainGrant(t, s, pgstore.GroupsGrantSQL, "shop", []string{"g0700", "g0900"}, 1))
}
