package pgstore

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const groupColumns = "tenant_id, id, name, coalesce(parent_id, ''), mentionable, state, created_at, updated_at, updated_by"

const liveGroupSQL = "SELECT 1 FROM groups WHERE tenant_id = $1 AND id = $2 AND state = $3"

const groupAncestorsContainSQL = `WITH RECURSIVE ancestors(id) AS (
	SELECT $2::text
	UNION
	SELECT g.parent_id FROM groups g JOIN ancestors a ON g.id = a.id
	WHERE g.tenant_id = $1 AND g.parent_id IS NOT NULL
)
SELECT EXISTS (SELECT 1 FROM ancestors WHERE id = $3)`

var (
	errGroupCycle       = fmt.Errorf("%w: group cycle", apperr.ErrFailedPrecondition)
	errGroupHasChildren = fmt.Errorf("%w: group has live child groups", apperr.ErrFailedPrecondition)
	errGroupListedTwice = fmt.Errorf("%w: group listed twice", apperr.ErrInvalidArgument)
)

func (s *Store) SetProfile(ctx context.Context, tenantID, accountID string, u store.ProfileUpdate, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			"UPDATE accounts SET display_name = coalesce($3, display_name), avatar_url = coalesce($4, avatar_url), updated_at = $5, updated_by = $6 WHERE tenant_id = $1 AND id = $2",
			tenantID, accountID, u.DisplayName, u.AvatarURL, audit.At, audit.Actor)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
}

func (s *Store) CreateGroup(ctx context.Context, g store.Group, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		if g.ParentID != "" {
			if err := lockGroupTree(ctx, tx, g.TenantID); err != nil {
				return err
			}
			if err := requireLiveGroup(ctx, tx, g.TenantID, g.ParentID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx,
			"INSERT INTO groups (tenant_id, id, name, parent_id, mentionable, state, created_at, updated_at, updated_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
			g.TenantID, g.ID, g.Name, nullable(g.ParentID), g.Mentionable, g.State, g.CreatedAt, g.UpdatedAt, g.UpdatedBy)
		return err
	})
}

func (s *Store) UpdateGroup(ctx context.Context, tenantID, id string, u store.GroupUpdate, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		if u.ParentID != nil {
			if err := lockGroupTree(ctx, tx, tenantID); err != nil {
				return err
			}
		}
		if err := lockLiveGroup(ctx, tx, tenantID, id); err != nil {
			return err
		}
		var parent *string
		if u.ParentID != nil && *u.ParentID != "" {
			parent = u.ParentID
			if err := requireLiveGroup(ctx, tx, tenantID, *parent); err != nil {
				return err
			}
			if err := refuseGroupCycle(ctx, tx, tenantID, id, *parent); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx,
			"UPDATE groups SET name = coalesce($3, name), parent_id = CASE WHEN $4::boolean THEN $5::text ELSE parent_id END, mentionable = coalesce($6, mentionable), updated_at = $7, updated_by = $8 WHERE tenant_id = $1 AND id = $2",
			tenantID, id, u.Name, u.ParentID != nil, parent, u.Mentionable, audit.At, audit.Actor)
		return err
	})
}

func (s *Store) DeleteGroup(ctx context.Context, tenantID, id string, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		if err := lockGroupTree(ctx, tx, tenantID); err != nil {
			return err
		}
		var children bool
		err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM groups WHERE tenant_id = $1 AND parent_id = $2 AND state = $3)",
			tenantID, id, store.GroupLive).Scan(&children)
		if err != nil {
			return err
		}
		if children {
			return errGroupHasChildren
		}
		tag, err := tx.Exec(ctx,
			"UPDATE groups SET state = $3, updated_at = $4, updated_by = $5 WHERE tenant_id = $1 AND id = $2 AND state = $6",
			tenantID, id, store.GroupDeleted, audit.At, audit.Actor, store.GroupLive)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		_, err = tx.Exec(ctx, "DELETE FROM account_groups WHERE tenant_id = $1 AND group_id = $2", tenantID, id)
		return err
	})
}

func (s *Store) SetAccountGroups(ctx context.Context, tenantID, accountID string, groupIDs []string, audit store.AuditEvent) error {
	ids, err := sortedUnique(groupIDs)
	if err != nil {
		return err
	}
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		var one int
		err := tx.QueryRow(ctx, "SELECT 1 FROM accounts WHERE tenant_id = $1 AND id = $2 FOR UPDATE", tenantID, accountID).Scan(&one)
		if err != nil {
			return err
		}
		if err := shareLiveGroups(ctx, tx, tenantID, ids); err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			"DELETE FROM account_groups WHERE tenant_id = $1 AND account_id = $2 AND group_id <> ALL($3)",
			tenantID, accountID, ids)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			"INSERT INTO account_groups (tenant_id, account_id, group_id) SELECT $1, $2, unnest($3::text[]) ON CONFLICT DO NOTHING",
			tenantID, accountID, ids)
		return err
	})
}

func (s *Store) Group(ctx context.Context, tenantID, id string) (store.Group, error) {
	var g store.Group
	err := s.pool.QueryRow(ctx, "SELECT "+groupColumns+" FROM groups WHERE tenant_id = $1 AND id = $2", tenantID, id).
		Scan(&g.TenantID, &g.ID, &g.Name, &g.ParentID, &g.Mentionable, &g.State, &g.CreatedAt, &g.UpdatedAt, &g.UpdatedBy)
	return g, s.mapErr(err)
}

func lockGroupTree(ctx context.Context, tx pgx.Tx, tenantID string) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(2, hashtext($1))", tenantID)
	return err
}

func requireLiveGroup(ctx context.Context, tx pgx.Tx, tenantID, id string) error {
	return liveGroup(ctx, tx, liveGroupSQL, tenantID, id)
}

func lockLiveGroup(ctx context.Context, tx pgx.Tx, tenantID, id string) error {
	return liveGroup(ctx, tx, liveGroupSQL+" FOR UPDATE", tenantID, id)
}

func liveGroup(ctx context.Context, tx pgx.Tx, sql, tenantID, id string) error {
	var one int
	return tx.QueryRow(ctx, sql, tenantID, id, store.GroupLive).Scan(&one)
}

func refuseGroupCycle(ctx context.Context, tx pgx.Tx, tenantID, id, parent string) error {
	var cycle bool
	if err := tx.QueryRow(ctx, groupAncestorsContainSQL, tenantID, parent, id).Scan(&cycle); err != nil {
		return err
	}
	if cycle {
		return errGroupCycle
	}
	return nil
}

func shareLiveGroups(ctx context.Context, tx pgx.Tx, tenantID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx,
		"SELECT id FROM groups WHERE tenant_id = $1 AND id = ANY($2) AND state = $3 ORDER BY id FOR SHARE",
		tenantID, ids, store.GroupLive)
	if err != nil {
		return err
	}
	live, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(live) != len(ids) {
		return pgx.ErrNoRows
	}
	return nil
}

func sortedUnique(ids []string) ([]string, error) {
	sorted := append([]string{}, ids...)
	slices.Sort(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return nil, errGroupListedTwice
		}
	}
	return sorted, nil
}
