package pgstore_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

const (
	planAccounts = 20000
	planPageSize = 1000
)

type planNode struct {
	NodeType      string     `json:"Node Type"`
	RelationName  string     `json:"Relation Name"`
	IndexName     string     `json:"Index Name"`
	ActualRows    float64    `json:"Actual Rows"`
	ActualLoops   float64    `json:"Actual Loops"`
	RemovedFilter float64    `json:"Rows Removed by Filter"`
	Plans         []planNode `json:"Plans"`
}

func seedAccountsWithoutStats(t *testing.T, s fixture, accounts int) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"accounts", "directory_changes"} {
		if _, err := s.raw.Exec(ctx, "ALTER TABLE "+table+" SET (autovacuum_enabled = false)"); err != nil {
			t.Fatalf("disable autovacuum on %s: %v", table, err)
		}
	}
	seedTenant(t, s, "shop")
	_, err := s.raw.Exec(ctx, `INSERT INTO accounts (tenant_id, id, username, display_name, state, password_hash,
		password_must_change, password_changed_at, ver, created_at, updated_at, updated_by)
		SELECT 'shop', 'u' || lpad(g::text, 6, '0'), 'user' || g, 'User ' || g, 1, 'x', false, now(), 1, now(), now(), 'test'
		FROM generate_series(1, $1::int) g`, accounts)
	if err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	var analyzed int
	err = s.raw.QueryRow(ctx, `SELECT count(*) FROM pg_stat_user_tables
		WHERE relname IN ('accounts', 'directory_changes') AND (last_analyze IS NOT NULL OR last_autoanalyze IS NOT NULL)`).Scan(&analyzed)
	if err != nil || analyzed != 0 {
		t.Fatalf("analyzed tables = %d, %v; want none", analyzed, err)
	}
}

func explainLoadPage(t *testing.T, s fixture, afterID string, limit int) planNode {
	t.Helper()
	var raw []byte
	err := s.raw.QueryRow(context.Background(), "EXPLAIN (ANALYZE, FORMAT JSON) "+pgstore.LoadPageSQL, "shop", afterID, limit).Scan(&raw)
	if err != nil {
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

func checkScansStayNearTheLimit(t *testing.T, n planNode, limit int) {
	t.Helper()
	if n.RelationName != "" {
		examined := (n.ActualRows + n.RemovedFilter) * n.ActualLoops
		if n.ActualLoops > float64(limit) || examined > float64(limit) {
			t.Errorf("%s on %s (index %q) examined %.0f rows in %.0f loops; want at most %d", n.NodeType, n.RelationName, n.IndexName, examined, n.ActualLoops, limit)
		}
	}
	for _, child := range n.Plans {
		checkScansStayNearTheLimit(t, child, limit)
	}
}

func TestLoadPageUsesTheAccountsKeyWithoutStats(t *testing.T) {
	s := openStore(t)
	seedAccountsWithoutStats(t, s, planAccounts)
	plan := explainLoadPage(t, s, "u018000", planPageSize)
	if plan.ActualRows != planPageSize {
		t.Fatalf("page rows = %.0f; want %d", plan.ActualRows, planPageSize)
	}
	checkScansStayNearTheLimit(t, plan, planPageSize)
}
