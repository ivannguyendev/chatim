package pgstore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

var benchTreeLevels = []int{4, 16, 64, 256, 1660}

const benchLevelSQL = `INSERT INTO groups (tenant_id, id, name, parent_id, state, created_at, updated_at, updated_by)
	SELECT 'shop', $1 || '-' || lpad(n::text, 4, '0'), 'G ' || n,
		CASE WHEN $2::text = '' THEN NULL WHEN $3::int = 0 THEN $2 ELSE $2 || '-' || lpad((n % $3::int)::text, 4, '0') END, 1, now(), now(), 'test'
	FROM generate_series(0, $4::int - 1) n`

const benchGroupRolesSQL = `INSERT INTO group_roles (tenant_id, group_id, role_id, created_at, created_by)
	SELECT 'shop', g.id, 'role-' || (g.r / 10 % 10), now(), 'test'
	FROM (SELECT id, row_number() OVER (ORDER BY md5(id)) AS r FROM groups WHERE tenant_id = 'shop') g
	WHERE g.r % 10 = 0`

func benchExec(b testing.TB, s fixture, sql string, args ...any) {
	b.Helper()
	if _, err := s.raw.Exec(context.Background(), sql, args...); err != nil {
		b.Fatalf("%s: %v", sql, err)
	}
}

func benchCount(b testing.TB, s fixture, sql string, args ...any) int {
	b.Helper()
	var n int
	if err := s.raw.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		b.Fatalf("%s: %v", sql, err)
	}
	return n
}

func seedLevel(b testing.TB, s fixture, prefix, parentPrefix string, parents, count int) {
	b.Helper()
	benchExec(b, s, benchLevelSQL, prefix, parentPrefix, parents, count)
}

func seedBenchRoles(b testing.TB, s fixture) {
	b.Helper()
	for i := range 10 {
		perms := make([]string, 0, 3)
		for k := range 3 {
			perms = append(perms, authz.IAMPermissions[(i+k*4)%len(authz.IAMPermissions)])
		}
		slices.Sort(perms)
		r := store.Role{TenantID: "shop", ID: fmt.Sprintf("role-%d", i), Name: "Role", Ver: 1, Permissions: perms, CreatedAt: t0, UpdatedAt: t0, UpdatedBy: "test"}
		if err := s.CreateRole(context.Background(), r, audit("role_create")); err != nil {
			b.Fatalf("CreateRole %s: %v", r.ID, err)
		}
	}
}

func rowsRead(n planNode) float64 {
	total := 0.0
	if n.RelationName != "" {
		total += (n.ActualRows + n.RemovedFilter) * n.ActualLoops
	}
	for _, child := range n.Plans {
		total += rowsRead(child)
	}
	return total
}

func explainRows(b testing.TB, s fixture, sql string, args ...any) float64 {
	b.Helper()
	var raw []byte
	if err := s.raw.QueryRow(context.Background(), "EXPLAIN (ANALYZE, FORMAT JSON) "+sql, args...).Scan(&raw); err != nil {
		b.Fatalf("explain: %v", err)
	}
	var plans []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
		b.Fatalf("parse plan: %v (%d plans)", err, len(plans))
	}
	return rowsRead(plans[0].Plan)
}

func BenchmarkAccountGrant2000Groups(b *testing.B) {
	ctx := context.Background()
	s := openStore(b)
	seedTenant(b, s, "shop")
	seedAccount(b, s, "shop", "binh", "binh")
	parent, parents := "", 0
	for level, count := range benchTreeLevels {
		prefix := fmt.Sprintf("l%d", level+1)
		seedLevel(b, s, prefix, parent, parents, count)
		parent, parents = prefix, count
	}
	seedBenchRoles(b, s)
	benchExec(b, s, benchGroupRolesSQL)
	joined := make([]string, 0, 10)
	for i := range 10 {
		joined = append(joined, fmt.Sprintf("l5-%04d", i*166))
	}
	if err := s.SetAccountGroups(ctx, "shop", "binh", joined, audit("account_groups_set")); err != nil {
		b.Fatalf("SetAccountGroups: %v", err)
	}
	benchExec(b, s, "ANALYZE")
	if n := benchCount(b, s, "SELECT count(*) FROM groups WHERE tenant_id = 'shop'"); n != 2000 {
		b.Fatalf("groups = %d; want 2000", n)
	}
	if n := benchCount(b, s, "SELECT count(*) FROM group_roles WHERE tenant_id = 'shop'"); n != 200 {
		b.Fatalf("groups with a role = %d; want 200", n)
	}
	rows := explainRows(b, s, pgstore.AccountGrantSQL, "shop", "binh", store.GroupLive)
	readOnly := store.AdminTxOptions{ReadOnly: true}
	grant := func(a store.AdminTx) {
		g, err := a.AccountGrant(ctx, "shop", "binh")
		if err != nil || len(g.Permissions) == 0 {
			b.Fatalf("AccountGrant = %+v, %v; want permissions", g, err)
		}
	}
	b.Run("query", func(b *testing.B) {
		err := s.RunAdmin(ctx, readOnly, func(a store.AdminTx) error {
			for b.Loop() {
				grant(a)
			}
			return nil
		})
		if err != nil {
			b.Fatalf("RunAdmin: %v", err)
		}
		b.ReportMetric(rows, "rows/op")
	})
	b.Run("transaction", func(b *testing.B) {
		for b.Loop() {
			err := s.RunAdmin(ctx, readOnly, func(a store.AdminTx) error {
				grant(a)
				return nil
			})
			if err != nil {
				b.Fatalf("RunAdmin: %v", err)
			}
		}
		b.ReportMetric(rows, "rows/op")
	})
}

func seedBigBranch(b testing.TB, s fixture) {
	b.Helper()
	for _, g := range []store.Group{group("left", ""), group("right", ""), group("big", "left")} {
		if err := s.CreateGroup(context.Background(), g, audit("group_create")); err != nil {
			b.Fatalf("CreateGroup %s: %v", g.ID, err)
		}
	}
	seedLevel(b, s, "big-a", "big", 0, 20)
	seedLevel(b, s, "big-b", "big-a", 20, 480)
	if n := benchCount(b, s, "SELECT count(*) FROM groups WHERE tenant_id = 'shop' AND 'big' = ANY(ancestors)"); n != 500 {
		b.Fatalf("descendants of big = %d; want 500", n)
	}
	benchExec(b, s, "ANALYZE")
}

func relationSIReadLocks(b testing.TB, a store.AdminTx) int {
	b.Helper()
	var n int
	err := pgstore.AdminTxConn(a).QueryRow(context.Background(),
		"SELECT count(*) FROM pg_locks WHERE pid = pg_backend_pid() AND mode = 'SIReadLock' AND locktype = 'relation' AND relation = 'groups'::regclass").Scan(&n)
	if err != nil {
		b.Fatalf("count SIRead locks: %v", err)
	}
	return n
}

func BenchmarkMoveGroupWith500Descendants(b *testing.B) {
	ctx := context.Background()
	s := openStore(b)
	seedTenant(b, s, "shop")
	seedBigBranch(b, s)
	parents := [2]string{"right", "left"}
	moves := 0
	move := func(check func(store.AdminTx)) {
		parent := parents[moves%2]
		err := s.RunAdmin(ctx, store.AdminTxOptions{Tenant: "shop", TreeLock: true}, func(a store.AdminTx) error {
			if err := a.UpdateGroup(ctx, "shop", "big", moveUnder(parent), t0, "test"); err != nil {
				return err
			}
			check(a)
			return nil
		})
		if err != nil {
			b.Fatalf("move big under %s: %v", parent, err)
		}
		moves++
	}
	promoted := 0
	move(func(a store.AdminTx) { promoted = relationSIReadLocks(b, a) })
	for b.Loop() {
		move(func(store.AdminTx) {})
	}
	b.ReportMetric(float64(b.Elapsed().Microseconds())/1000/float64(b.N), "ms/op")
	b.ReportMetric(float64(promoted), "groups-relation-siread")
	want := parents[(moves-1)%2]
	if n := benchCount(b, s, "SELECT count(*) FROM groups WHERE tenant_id = 'shop' AND ancestors[1] = $1 AND 'big' = ANY(ancestors)", want); n != 500 {
		b.Fatalf("descendants under %s = %d; want 500", want, n)
	}
}
