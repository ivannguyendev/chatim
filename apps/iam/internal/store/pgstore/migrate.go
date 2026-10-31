package pgstore

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const migrationLockID = 7_311_405_201

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func LatestVersion() int {
	all, err := migrations()
	if err != nil || len(all) == 0 {
		return 0
	}
	return all[len(all)-1].version
}

func (s *Store) Migrate(ctx context.Context, apply bool) ([]string, error) {
	return s.migrate(ctx, apply, math.MaxInt)
}

func (s *Store) migrate(ctx context.Context, apply bool, upTo int) ([]string, error) {
	all, err := migrations()
	if err != nil {
		return nil, err
	}
	var pending []string
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL)"); err != nil {
			return err
		}
		applied, err := appliedVersions(ctx, tx)
		if err != nil {
			return err
		}
		for _, m := range all {
			if m.version > upTo {
				break
			}
			if applied[m.version] {
				continue
			}
			pending = append(pending, m.name)
			if !apply {
				continue
			}
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return fmt.Errorf("apply %s: %w", m.name, err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)", m.version, time.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, s.mapErr(err)
	}
	return pending, nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.pool.QueryRow(ctx, "SELECT coalesce(max(version), 0) FROM schema_migrations").Scan(&v)
	if err != nil {
		return 0, s.mapErr(err)
	}
	return v, nil
}

func appliedVersions(ctx context.Context, tx pgx.Tx) (map[int]bool, error) {
	rows, err := tx.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, err
	}
	applied := make(map[int]bool, len(versions))
	for _, v := range versions {
		applied[v] = true
	}
	return applied, nil
}

func migrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	out := make([]migration, 0, len(entries))
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(prefix)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migration %s: name must start with a positive number and _", e.Name())
		}
		body, err := fs.ReadFile(migrationFiles, "migrations/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	slices.SortFunc(out, func(a, b migration) int { return a.version - b.version })
	return out, nil
}
