package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

func (s *Store) CreateTenant(ctx context.Context, t store.Tenant, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO tenants (id, name, state, ver, created_at, updated_at, updated_by) VALUES ($1, $2, $3, $4, $5, $6, $7)",
			t.ID, t.Name, t.State, t.Ver, t.CreatedAt, t.UpdatedAt, t.UpdatedBy)
		return err
	})
}

func (s *Store) SetTenantState(ctx context.Context, id string, state store.State, r store.Revocation, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			"UPDATE tenants SET state = $2, ver = ver + 1, updated_at = $3, updated_by = $4 WHERE id = $1",
			id, state, r.At, r.By)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if state != store.StateDisabled {
			return nil
		}
		_, err = tx.Exec(ctx, "UPDATE tenants SET sessions_revoked_at = clock_timestamp() WHERE id = $1", id)
		return err
	})
}

func (s *Store) SetUserState(ctx context.Context, tenantID, id string, state store.State, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			"UPDATE users SET state = $3, ver = ver + 1, updated_at = $4, updated_by = $5 WHERE tenant_id = $1 AND id = $2",
			tenantID, id, state, audit.At, audit.Actor)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
}
