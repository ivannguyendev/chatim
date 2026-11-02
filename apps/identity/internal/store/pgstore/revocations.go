package pgstore

import (
	"cmp"
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func (s *Store) Revocations(ctx context.Context) (revocation.Snapshot, error) {
	var snap revocation.Snapshot
	rows, err := s.pool.Query(ctx, "SELECT session_id, tenant_id, expires_at FROM revoked_sessions WHERE expires_at > now()")
	if err != nil {
		return snap, s.mapErr(err)
	}
	snap.Sessions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (revocation.Session, error) {
		var e revocation.Session
		err := r.Scan(&e.ID, &e.Tenant, &e.ExpiresAt)
		return e, err
	})
	if err != nil {
		return snap, s.mapErr(err)
	}
	rows, err = s.pool.Query(ctx,
		"SELECT id, sessions_revoked_at, sessions_revoked_at + $1 FROM tenants WHERE sessions_revoked_at + $1 > now()", s.keep)
	if err != nil {
		return snap, s.mapErr(err)
	}
	snap.Tenants, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (revocation.Tenant, error) {
		var e revocation.Tenant
		err := r.Scan(&e.ID, &e.RevokedBefore, &e.ExpiresAt)
		return e, err
	})
	if err != nil {
		return snap, s.mapErr(err)
	}
	slices.SortFunc(snap.Sessions, func(a, b revocation.Session) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(snap.Tenants, func(a, b revocation.Tenant) int { return cmp.Compare(a.ID, b.ID) })
	return snap, nil
}

func (s *Store) DeleteExpiredRevocations(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, "DELETE FROM revoked_sessions WHERE expires_at < now()")
	if err != nil {
		return 0, s.mapErr(err)
	}
	return tag.RowsAffected(), nil
}
