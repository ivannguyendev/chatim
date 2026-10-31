package pgstore

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const accountColumns = "tenant_id, id, username, coalesce(email, ''), state, password_hash, password_must_change, password_changed_at, failed_login_count, next_login_at, ver, created_at, updated_at, updated_by"

func (s *Store) CreateTenant(ctx context.Context, t store.Tenant, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO tenants (id, name, state, ver, created_at, updated_at, updated_by) VALUES ($1, $2, $3, $4, $5, $6, $7)",
			t.ID, t.Name, t.State, t.Ver, t.CreatedAt, t.UpdatedAt, t.UpdatedBy)
		return err
	})
}

func (s *Store) TenantByID(ctx context.Context, id string) (store.Tenant, error) {
	var t store.Tenant
	err := s.pool.QueryRow(ctx,
		"SELECT id, name, state, ver, created_at, updated_at, updated_by FROM tenants WHERE id = $1", id).
		Scan(&t.ID, &t.Name, &t.State, &t.Ver, &t.CreatedAt, &t.UpdatedAt, &t.UpdatedBy)
	return t, s.mapErr(err)
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
		if err := revokeSessions(ctx, tx, r, "tenant_id = $4", id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE tenants SET sessions_revoked_at = clock_timestamp() WHERE id = $1", id)
		return err
	})
}

func (s *Store) CreateAccount(ctx context.Context, a store.Account, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO accounts (tenant_id, id, username, display_name, email, state, password_hash, password_must_change, password_changed_at, ver, created_at, updated_at, updated_by) VALUES ($1, $2, $3, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)",
			a.TenantID, a.ID, a.Username, nullable(a.Email), a.State, a.PasswordHash, a.PasswordMustChange, a.PasswordChangedAt, a.Ver, a.CreatedAt, a.UpdatedAt, a.UpdatedBy)
		return err
	})
}

func (s *Store) AccountByUsername(ctx context.Context, tenantID, username string) (store.Account, error) {
	return s.account(ctx, "tenant_id = $1 AND username = $2", tenantID, username)
}

func (s *Store) AccountByID(ctx context.Context, tenantID, id string) (store.Account, error) {
	return s.account(ctx, "tenant_id = $1 AND id = $2", tenantID, id)
}

func (s *Store) account(ctx context.Context, where string, args ...any) (store.Account, error) {
	var a store.Account
	var next *time.Time
	err := s.pool.QueryRow(ctx, "SELECT "+accountColumns+" FROM accounts WHERE "+where, args...).
		Scan(&a.TenantID, &a.ID, &a.Username, &a.Email, &a.State, &a.PasswordHash, &a.PasswordMustChange,
			&a.PasswordChangedAt, &a.FailedLoginCount, &next, &a.Ver, &a.CreatedAt, &a.UpdatedAt, &a.UpdatedBy)
	if next != nil {
		a.NextLoginAt = *next
	}
	return a, s.mapErr(err)
}

func (s *Store) SetAccountState(ctx context.Context, tenantID, id string, state store.State, r store.Revocation, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			"UPDATE accounts SET state = $3, ver = ver + 1, updated_at = $4, updated_by = $5 WHERE tenant_id = $1 AND id = $2",
			tenantID, id, state, r.At, r.By)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if state != store.StateDisabled {
			return nil
		}
		return s.revokeWhere(ctx, tx, r, "tenant_id = $4 AND account_id = $5", tenantID, id)
	})
}

func (s *Store) RecordLoginFailure(ctx context.Context, tenantID, accountID string, nextLoginAt func(failed int) time.Time, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		var failed int
		err := tx.QueryRow(ctx,
			"UPDATE accounts SET failed_login_count = failed_login_count + 1 WHERE tenant_id = $1 AND id = $2 RETURNING failed_login_count",
			tenantID, accountID).Scan(&failed)
		if err != nil {
			return err
		}
		var next *time.Time
		if at := nextLoginAt(failed); !at.IsZero() {
			next = &at
		}
		_, err = tx.Exec(ctx, "UPDATE accounts SET next_login_at = $3 WHERE tenant_id = $1 AND id = $2", tenantID, accountID, next)
		return err
	})
}

func (s *Store) SetPassword(ctx context.Context, c store.PasswordChange, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			"UPDATE accounts SET password_hash = $4, password_must_change = $5, password_changed_at = $6, failed_login_count = 0, next_login_at = NULL, ver = ver + 1, updated_at = $6, updated_by = $7 WHERE tenant_id = $1 AND id = $2 AND ver = $3",
			c.TenantID, c.AccountID, c.ExpectVer, c.Hash, c.MustChange, c.Revocation.At, c.Revocation.By)
		if err != nil {
			return err
		}
		if err := expectOne(tag); err != nil {
			return err
		}
		return s.revokeWhere(ctx, tx, c.Revocation, "tenant_id = $4 AND account_id = $5", c.TenantID, c.AccountID)
	})
}

const revokeSessionsSQL = "UPDATE sessions SET state = 2, revoked_at = $1, revoked_by = $2, revoked_reason = $3 WHERE state = 1 AND "

func revokeSessions(ctx context.Context, tx pgx.Tx, r store.Revocation, where string, args ...any) error {
	_, err := tx.Exec(ctx, revokeSessionsSQL+where, append([]any{r.At, r.By, r.Reason}, args...)...)
	return err
}

func (s *Store) revokeWhere(ctx context.Context, tx pgx.Tx, r store.Revocation, where string, args ...any) error {
	_, err := s.revokeAndRecord(ctx, tx, r, where, args...)
	return err
}

func (s *Store) revokeAndRecord(ctx context.Context, tx pgx.Tx, r store.Revocation, where string, args ...any) (int, error) {
	keepAt := len(args) + 4
	sql := "WITH r AS (" + revokeSessionsSQL + where + " RETURNING id, tenant_id), " +
		"t AS (SELECT clock_timestamp() AS at), " +
		"i AS (INSERT INTO revoked_sessions (session_id, tenant_id, revoked_at, expires_at) " +
		"SELECT r.id, r.tenant_id, t.at, t.at + $" + strconv.Itoa(keepAt) + " FROM r, t ON CONFLICT (session_id) DO NOTHING) " +
		"SELECT count(*) FROM r"
	params := append([]any{r.At, r.By, r.Reason}, args...)
	params = append(params, s.keep)
	var revoked int
	err := tx.QueryRow(ctx, sql, params...).Scan(&revoked)
	return revoked, err
}
