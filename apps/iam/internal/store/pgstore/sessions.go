package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const sessionColumns = "id, tenant_id, account_id, state, device_name, user_agent, created_at, last_used_at, idle_expires_at, expires_at, refresh_ver, refresh_hash, refresh_history, dpop_jkt, dpop_recent_jti, revoked_at, coalesce(revoked_by, ''), coalesce(revoked_reason, '')"

type historyRow struct {
	Hash       []byte    `json:"hash"`
	ReplacedAt time.Time `json:"replaced_at"`
}

func (s *Store) CreateSession(ctx context.Context, n store.NewSession, audit store.AuditEvent) error {
	sess := n.Session
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		var tenantState store.State
		if err := tx.QueryRow(ctx, "SELECT state FROM tenants WHERE id = $1 FOR SHARE", sess.TenantID).Scan(&tenantState); err != nil {
			return err
		}
		if tenantState != store.StateActive {
			return errConflict
		}
		tag, err := tx.Exec(ctx,
			"UPDATE accounts SET failed_login_count = 0, next_login_at = NULL, password_hash = coalesce(nullif($4, ''), password_hash), ver = ver + (CASE WHEN $4 = '' THEN 0 ELSE 1 END) WHERE tenant_id = $1 AND id = $2 AND ver = $3 AND state = 1",
			sess.TenantID, sess.AccountID, n.AccountVer, n.Rehash)
		if err != nil {
			return err
		}
		if err := expectOne(tag); err != nil {
			return err
		}
		history, err := encodeHistory(sess.History)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			"INSERT INTO sessions (id, tenant_id, account_id, state, device_name, user_agent, created_at, last_used_at, idle_expires_at, expires_at, refresh_ver, refresh_hash, refresh_history, dpop_jkt, dpop_recent_jti) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)",
			sess.ID, sess.TenantID, sess.AccountID, sess.State, sess.DeviceName, sess.UserAgent, sess.CreatedAt, sess.LastUsedAt,
			sess.IdleExpiresAt, sess.ExpiresAt, sess.RefreshVer, sess.RefreshHash, history, sess.KeyThumbprint, nonNil(sess.RecentJTI))
		return err
	})
}

func (s *Store) SessionByID(ctx context.Context, id string) (store.Session, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE id = $1", id)
	sess, err := scanSession(row)
	return sess, s.mapErr(err)
}

func (s *Store) ActiveSessions(ctx context.Context, tenantID, accountID string) ([]store.Session, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE tenant_id = $1 AND account_id = $2 AND state = 1 ORDER BY last_used_at DESC",
		tenantID, accountID)
	if err != nil {
		return nil, s.mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (store.Session, error) { return scanSession(r) })
	return out, s.mapErr(err)
}

func (s *Store) RotateRefresh(ctx context.Context, r store.Rotation, audit store.AuditEvent) error {
	history, err := encodeHistory(r.History)
	if err != nil {
		return s.mapErr(err)
	}
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			"UPDATE sessions SET refresh_ver = refresh_ver + 1, refresh_hash = $3, refresh_history = $4, dpop_recent_jti = $5, idle_expires_at = $6, last_used_at = $7 WHERE id = $1 AND state = 1 AND refresh_ver = $2",
			r.SessionID, r.ExpectVer, r.RefreshHash, history, nonNil(r.RecentJTI), r.IdleExpiresAt, r.LastUsedAt)
		if err != nil {
			return err
		}
		return expectOne(tag)
	})
}

func (s *Store) RevokeSession(ctx context.Context, id string, r store.Revocation, audit store.AuditEvent) (bool, error) {
	revoked := false
	err := s.mapErr(s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			"UPDATE sessions SET state = 2, revoked_at = $2, revoked_by = $3, revoked_reason = $4 WHERE id = $1 AND state = 1",
			id, r.At, r.By, r.Reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		revoked = true
		return insertAudit(ctx, tx, audit)
	}))
	return revoked, err
}

func scanSession(row pgx.Row) (store.Session, error) {
	var sess store.Session
	var history []byte
	var revokedAt *time.Time
	err := row.Scan(&sess.ID, &sess.TenantID, &sess.AccountID, &sess.State, &sess.DeviceName, &sess.UserAgent,
		&sess.CreatedAt, &sess.LastUsedAt, &sess.IdleExpiresAt, &sess.ExpiresAt, &sess.RefreshVer, &sess.RefreshHash,
		&history, &sess.KeyThumbprint, &sess.RecentJTI, &revokedAt, &sess.RevokedBy, &sess.RevokedReason)
	if err != nil {
		return store.Session{}, err
	}
	if revokedAt != nil {
		sess.RevokedAt = *revokedAt
	}
	sess.History, err = decodeHistory(history)
	return sess, err
}

func encodeHistory(h []session.HistoryEntry) ([]byte, error) {
	rows := make([]historyRow, len(h))
	for i, e := range h {
		rows[i] = historyRow{Hash: e.Hash, ReplacedAt: e.ReplacedAt}
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("encode refresh history: %w", err)
	}
	return b, nil
}

func decodeHistory(b []byte) ([]session.HistoryEntry, error) {
	var rows []historyRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("decode refresh history: %w", err)
	}
	out := make([]session.HistoryEntry, len(rows))
	for i, r := range rows {
		out[i] = session.HistoryEntry{Hash: r.Hash, ReplacedAt: r.ReplacedAt}
	}
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
