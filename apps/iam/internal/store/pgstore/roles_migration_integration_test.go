package pgstore_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

func queryStrings(t *testing.T, conn *pgx.Conn, sql string, args ...any) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return out
}

func ancestorsOf(t *testing.T, conn *pgx.Conn, id string) []string {
	t.Helper()
	var path []string
	err := conn.QueryRow(context.Background(), "SELECT ancestors FROM groups WHERE tenant_id = 'shop' AND id = $1", id).Scan(&path)
	if err != nil {
		t.Fatalf("ancestors of %s: %v", id, err)
	}
	return path
}

func wantAncestors(t *testing.T, conn *pgx.Conn, want map[string][]string) {
	t.Helper()
	for id, path := range want {
		if got := ancestorsOf(t, conn, id); !slices.Equal(got, path) {
			t.Fatalf("ancestors of %s = %v; want %v", id, got, path)
		}
	}
}

func wantPgError(t *testing.T, err error, code, constraint, step string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code || pgErr.ConstraintName != constraint {
		t.Fatalf("%s: err = %v; want %s on %s", step, err, code, constraint)
	}
}

func feedSnapshot(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	return queryStrings(t, conn, "SELECT format('%s/%s/%s/%s/%s', kind, tenant_id, id, ver, change_xid) FROM directory_changes ORDER BY kind, tenant_id, id")
}

func TestCatalogRowsMatchTheCode(t *testing.T) {
	s := openStore(t)
	got := queryStrings(t, s.raw, "SELECT permission FROM client_permissions WHERE client_id = 'iam' ORDER BY permission")
	want := slices.Clone(authz.IAMPermissions)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("iam catalog = %v; want %v", got, want)
	}
	_, err := s.raw.Exec(context.Background(), "INSERT INTO client_permissions (permission, client_id, description) VALUES ('chatim:rooms.read', 'iam', 'x')")
	wantPgError(t, err, "23514", "client_permissions_client_prefix", "permission of another client")
}

func TestNewTenantGetsTenantAdmin(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	var name, updatedBy string
	var builtin bool
	var ver int64
	var createdAt, updatedAt time.Time
	err := s.raw.QueryRow(context.Background(),
		"SELECT name, builtin, ver, created_at, updated_at, updated_by FROM roles WHERE tenant_id = 'shop' AND id = $1", authz.TenantAdminRole).
		Scan(&name, &builtin, &ver, &createdAt, &updatedAt, &updatedBy)
	if err != nil {
		t.Fatalf("read tenant-admin: %v", err)
	}
	if name != "Tenant admin" || !builtin || ver != 1 || !createdAt.Equal(t0) || !updatedAt.Equal(t0) || updatedBy != "iam" {
		t.Fatalf("tenant-admin = %q builtin=%v ver=%d created=%v updated=%v by=%q", name, builtin, ver, createdAt, updatedAt, updatedBy)
	}
	if perms := queryStrings(t, s.raw, "SELECT permission FROM role_permissions WHERE tenant_id = 'shop'"); len(perms) != 0 {
		t.Fatalf("tenant-admin permissions = %v; want none", perms)
	}
}

func TestMigrationBackfillsTenantAdminAndAncestors(t *testing.T) {
	ctx := context.Background()
	uri := testDatabaseURI(t)
	s, err := pgstore.Open(ctx, uri, pgstore.Options{MaxConns: 2, RevocationKeep: testKeep})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	if _, err := s.MigrateUpTo(ctx, 3); err != nil {
		t.Fatalf("MigrateUpTo 3: %v", err)
	}
	f := fixture{Store: s, raw: connect(t, uri), uri: uri}
	exec(t, f.raw, "INSERT INTO tenants (id, name, state, ver, created_at, updated_at, updated_by) VALUES ('shop', 'shop', 1, 1, now(), now(), 'test')")
	insertGroup(t, f, "company", "Company", nil)
	insertGroup(t, f, "it", "IT", "company")
	insertGroup(t, f, "it-hcm", "IT HCM", "it")
	insertGroup(t, f, "old", "Old", "it")
	exec(t, f.raw, "UPDATE groups SET state = $1 WHERE tenant_id = 'shop' AND id = 'old'", store.GroupDeleted)
	insertGroup(t, f, "old-team", "Old team", "old")
	before := feedSnapshot(t, f.raw)
	pending, err := s.Migrate(ctx, true)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !slices.Equal(pending, []string{"0004_roles.sql"}) {
		t.Fatalf("pending = %v; want [0004_roles.sql]", pending)
	}
	if got := queryStrings(t, f.raw, "SELECT tenant_id || '/' || id FROM roles WHERE builtin"); !slices.Equal(got, []string{"shop/tenant-admin"}) {
		t.Fatalf("builtin roles = %v; want [shop/tenant-admin]", got)
	}
	wantAncestors(t, f.raw, map[string][]string{
		"company":  {},
		"it":       {"company"},
		"it-hcm":   {"company", "it"},
		"old":      {"company", "it"},
		"old-team": {"company", "it", "old"},
	})
	if after := feedSnapshot(t, f.raw); !slices.Equal(after, before) {
		t.Fatalf("directory_changes after migrate = %v; want %v", after, before)
	}
}

func TestMovingAGroupRewritesTheSubtreeWithOneFeedRecord(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	createGroups(t, s, group("company", ""), group("hr", "company"), group("it", "company"),
		group("it-hcm", "it"), group("it-hcm-dev", "it-hcm"), group("it-hn", "it"))
	later := audit("group_update")
	later.At = t0.Add(time.Hour)
	if err := s.UpdateGroup(ctx, "shop", "it", moveUnder("hr"), later); err != nil {
		t.Fatalf("move it under hr: %v", err)
	}
	wantAncestors(t, s.raw, map[string][]string{
		"company":    {},
		"hr":         {"company"},
		"it":         {"company", "hr"},
		"it-hcm":     {"company", "hr", "it"},
		"it-hcm-dev": {"company", "hr", "it", "it-hcm"},
		"it-hn":      {"company", "hr", "it"},
	})
	wantVer(t, s, groupKind, "it", 2, "moved group")
	for _, id := range []string{"it-hcm", "it-hcm-dev", "it-hn", "hr", "company"} {
		wantVer(t, s, groupKind, id, 1, "untouched "+id)
	}
	if moved := queryStrings(t, s.raw, "SELECT id FROM groups WHERE updated_at <> $1 ORDER BY id", t0); len(moved) != 1 || moved[0] != "it" {
		t.Fatalf("groups with a new updated_at = %v; want [it]", moved)
	}
	name := "IT Ha Noi"
	if err := s.UpdateGroup(ctx, "shop", "it-hn", store.GroupUpdate{Name: &name}, audit("group_update")); err != nil {
		t.Fatalf("rename it-hn: %v", err)
	}
	wantAncestors(t, s.raw, map[string][]string{"it-hn": {"company", "hr", "it"}})
	if err := s.UpdateGroup(ctx, "shop", "it", moveUnder(""), audit("group_update")); err != nil {
		t.Fatalf("make it a root: %v", err)
	}
	wantAncestors(t, s.raw, map[string][]string{"it": {}, "it-hcm": {"it"}, "it-hcm-dev": {"it", "it-hcm"}, "it-hn": {"it"}})
}

type groupModel map[string]string

func (m groupModel) wouldCycle(id, parent string) bool {
	for at := parent; at != ""; at = m[at] {
		if at == id {
			return true
		}
	}
	return false
}

func (m groupModel) pick(r *rand.Rand) string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids[r.IntN(len(ids))]
}

func wantAncestorsMatchParents(t *testing.T, conn *pgx.Conn, step int) {
	t.Helper()
	const sql = `WITH RECURSIVE paths(id, ancestors) AS (
	SELECT id, '{}'::text[] FROM groups WHERE tenant_id = 'shop' AND parent_id IS NULL
	UNION ALL
	SELECT g.id, p.ancestors || p.id FROM groups g JOIN paths p ON g.tenant_id = 'shop' AND g.parent_id = p.id
)
SELECT g.id FROM groups g LEFT JOIN paths p ON p.id = g.id
WHERE g.tenant_id = 'shop' AND g.ancestors IS DISTINCT FROM p.ancestors ORDER BY g.id`
	if wrong := queryStrings(t, conn, sql); len(wrong) != 0 {
		t.Fatalf("after op %d: ancestors differ from parent_id for %v", step, wrong)
	}
}

func TestAncestorsFollowParentThroughRandomOps(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	r := rand.New(rand.NewPCG(3, 4))
	model := groupModel{}
	cycles := 0
	for step := 1; step <= 300; step++ {
		switch op := r.IntN(3); {
		case len(model) < 2 || (op == 0 && len(model) < 40):
			id := fmt.Sprintf("g%02d", len(model))
			var parent any
			if len(model) > 0 && r.IntN(4) > 0 {
				p := model.pick(r)
				parent, model[id] = p, p
			} else {
				model[id] = ""
			}
			insertGroup(t, s, id, id, parent)
		case op == 2:
			exec(t, s.raw, "UPDATE groups SET state = $2, updated_at = now() WHERE tenant_id = 'shop' AND id = $1", model.pick(r), store.GroupDeleted)
		default:
			id, parent := model.pick(r), ""
			if r.IntN(5) > 0 {
				parent = model.pick(r)
			}
			_, err := s.raw.Exec(ctx, "UPDATE groups SET parent_id = $2 WHERE tenant_id = 'shop' AND id = $1", id, pgNullable(parent))
			if model.wouldCycle(id, parent) {
				wantPgError(t, err, "23514", "groups_no_cycle", fmt.Sprintf("op %d move %s under %s", step, id, parent))
				cycles++
				continue
			}
			if err != nil {
				t.Fatalf("op %d move %s under %q: %v", step, id, parent, err)
			}
			model[id] = parent
		}
		if step%20 == 0 {
			wantAncestorsMatchParents(t, s.raw, step)
		}
	}
	if len(model) != 40 || cycles == 0 {
		t.Fatalf("groups = %d, refused cycles = %d; want 40 and some cycles", len(model), cycles)
	}
}

func pgNullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestTriggerRefusesACycle(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	insertGroup(t, s, "company", "Company", nil)
	insertGroup(t, s, "it", "IT", "company")
	insertGroup(t, s, "it-hcm", "IT HCM", "it")
	insertGroup(t, s, "it-hcm-dev", "IT HCM dev", "it-hcm")
	_, err := s.raw.Exec(ctx, "UPDATE groups SET parent_id = 'it-hcm-dev' WHERE tenant_id = 'shop' AND id = 'it'")
	wantPgError(t, err, "23514", "groups_no_cycle", "move into a grandchild")
	_, err = s.raw.Exec(ctx, "UPDATE groups SET parent_id = 'it' WHERE tenant_id = 'shop' AND id = 'it'")
	wantPgError(t, err, "23514", "groups_no_cycle", "parent of itself")
	_, err = s.raw.Exec(ctx, "INSERT INTO groups (tenant_id, id, name, parent_id, state, created_at, updated_at, updated_by) VALUES ('shop', 'loop', 'Loop', 'loop', 1, now(), now(), 'test')")
	wantPgError(t, err, "23514", "groups_no_cycle", "new group under itself")
	wantAncestors(t, s.raw, map[string][]string{"it": {"company"}, "it-hcm-dev": {"company", "it", "it-hcm"}})
}

func TestRolePermissionMustBeInTheCatalog(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedTenant(t, s, "shop")
	exec(t, s.raw, "INSERT INTO roles (tenant_id, id, name, builtin, ver, created_at, updated_at, updated_by) VALUES ('shop', 'hr-admin', 'HR admin', false, 1, now(), now(), 'test')")
	exec(t, s.raw, "INSERT INTO role_permissions (tenant_id, role_id, permission) VALUES ('shop', 'hr-admin', 'iam:accounts.read')")
	_, err := s.raw.Exec(ctx, "INSERT INTO role_permissions (tenant_id, role_id, permission) VALUES ('shop', 'hr-admin', 'iam:acounts.read')")
	wantPgError(t, err, "23503", "role_permissions_permission_fkey", "typo in permission")
}
