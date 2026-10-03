package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const columnDefs = `room_id bigint NOT NULL, thread_root bigint NOT NULL, seq bigint NOT NULL,
f text NOT NULL, p bigint NOT NULL, kind smallint NOT NULL, body text NOT NULL, ts timestamptz NOT NULL,
PRIMARY KEY (room_id, thread_root, seq)`

var columns = []string{"room_id", "thread_root", "seq", "f", "p", "kind", "body", "ts"}

func createTable(ctx context.Context, pool *pgxpool.Pool, table string, partitions int) error {
	id := pgx.Identifier{table}.Sanitize()
	if partitions <= 0 {
		_, err := pool.Exec(ctx, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", id, columnDefs))
		return err
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s) PARTITION BY HASH (room_id)", id, columnDefs)); err != nil {
		return err
	}
	for i := 0; i < partitions; i++ {
		part := pgx.Identifier{fmt.Sprintf("%s_p%d", table, i)}.Sanitize()
		stmt := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES WITH (MODULUS %d, REMAINDER %d)", part, id, partitions, i)
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func printStorage(ctx context.Context, pool *pgxpool.Pool, table string) error {
	id := pgx.Identifier{table}.Sanitize()
	var heap, index int64
	sizes := `WITH rel AS (
  SELECT oid FROM pg_class WHERE oid = $1::regclass AND relkind = 'r'
  UNION ALL
  SELECT inhrelid FROM pg_inherits WHERE inhparent = $1::regclass)
SELECT coalesce(sum(pg_table_size(oid)), 0), coalesce(sum(pg_indexes_size(oid)), 0) FROM rel`
	if err := pool.QueryRow(ctx, sizes, table).Scan(&heap, &index); err != nil {
		return fmt.Errorf("table sizes: %w", err)
	}
	var rows, logical int64
	if err := pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*), coalesce(sum(pg_column_size(t.*)), 0) FROM %s t", id)).Scan(&rows, &logical); err != nil {
		return fmt.Errorf("row stats: %w", err)
	}
	if rows == 0 {
		fmt.Println("table is empty")
		return nil
	}
	total := heap + index
	perRow := float64(total) / float64(rows)
	fmt.Printf("rows=%d logical=%.1fMB heap=%.1fMB index=%.1fMB on-disk=%.1fMB logical/row=%.0fB heap/row=%.0fB index/row=%.0fB disk/row=%.0fB\n",
		rows, mb(logical), mb(heap), mb(index), mb(total), float64(logical)/float64(rows), float64(heap)/float64(rows), float64(index)/float64(rows), perRow)
	for _, n := range []float64{5e9, 20e9} {
		fmt.Printf("projected on-disk for %.0f billion messages: %.2f TB (before WAL, replicas)\n", n/1e9, n*perRow/1e12)
	}
	return nil
}

func mb(b int64) float64 { return float64(b) / (1 << 20) }

type planNode struct {
	NodeType  string     `json:"Node Type"`
	IndexName string     `json:"Index Name"`
	Plans     []planNode `json:"Plans"`
}

func (n planNode) stages() []string {
	s := n.NodeType
	if n.IndexName != "" {
		s += " (" + n.IndexName + ")"
	}
	out := []string{s}
	for _, c := range n.Plans {
		out = append(out, c.stages()...)
	}
	return out
}

func explain(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) ([]string, error) {
	var raw string
	all := append([]any{pgx.QueryExecModeSimpleProtocol}, args...)
	if err := pool.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql, all...).Scan(&raw); err != nil {
		return nil, fmt.Errorf("explain: %w", err)
	}
	var plans []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal([]byte(raw), &plans); err != nil || len(plans) == 0 {
		return nil, fmt.Errorf("decode explain: %v", err)
	}
	return plans[0].Plan.stages(), nil
}
