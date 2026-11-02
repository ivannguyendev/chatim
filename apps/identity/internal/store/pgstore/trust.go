package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const trustedKeyColumns = "issuer, key_id, algorithm, public_key, state, created_at, created_by"

func (s *Store) TrustedIssuers(ctx context.Context) ([]store.TrustedIssuer, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT i.issuer, i.tenant_id, i.state, t.state FROM trusted_issuers i JOIN tenants t ON t.id = i.tenant_id ORDER BY i.issuer")
	if err != nil {
		return nil, s.mapErr(err)
	}
	issuers, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (store.TrustedIssuer, error) {
		var i store.TrustedIssuer
		err := r.Scan(&i.Issuer, &i.TenantID, &i.IssuerState, &i.TenantState)
		return i, err
	})
	return issuers, s.mapErr(err)
}

func (s *Store) TrustedKeys(ctx context.Context) ([]store.TrustedKey, error) {
	return s.trustedKeys(ctx, "SELECT "+trustedKeyColumns+" FROM trusted_keys WHERE state = $1 ORDER BY issuer, key_id", store.StateActive)
}

func (s *Store) TrustedKeysOf(ctx context.Context, issuer string) ([]store.TrustedKey, error) {
	return s.trustedKeys(ctx, "SELECT "+trustedKeyColumns+" FROM trusted_keys WHERE issuer = $1 ORDER BY created_at, key_id", issuer)
}

func (s *Store) trustedKeys(ctx context.Context, query string, arg any) ([]store.TrustedKey, error) {
	rows, err := s.pool.Query(ctx, query, arg)
	if err != nil {
		return nil, s.mapErr(err)
	}
	keys, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (store.TrustedKey, error) {
		var k store.TrustedKey
		err := r.Scan(&k.Issuer, &k.KeyID, &k.Algorithm, &k.PublicKey, &k.State, &k.CreatedAt, &k.CreatedBy)
		return k, err
	})
	return keys, s.mapErr(err)
}

func (s *Store) CreateTrustedIssuer(ctx context.Context, issuer, tenantID string, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO trusted_issuers (issuer, tenant_id, state, ver, created_at, updated_at, updated_by) VALUES ($1, $2, $3, 1, $4, $4, $5)",
			issuer, tenantID, store.StateActive, audit.At, audit.Actor)
		return err
	})
}

func (s *Store) SetTrustedIssuerState(ctx context.Context, issuer string, state store.State, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		return execOne(ctx, tx,
			"UPDATE trusted_issuers SET state = $2, ver = ver + 1, updated_at = $3, updated_by = $4 WHERE issuer = $1",
			issuer, state, audit.At, audit.Actor)
	})
}

func (s *Store) AddTrustedKey(ctx context.Context, k store.TrustedKey, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO trusted_keys ("+trustedKeyColumns+") VALUES ($1, $2, $3, $4, $5, $6, $7)",
			k.Issuer, k.KeyID, k.Algorithm, k.PublicKey, store.StateActive, audit.At, audit.Actor)
		return err
	})
}

func (s *Store) RemoveTrustedKey(ctx context.Context, issuer, keyID string, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		return execOne(ctx, tx,
			"UPDATE trusted_keys SET state = $3 WHERE issuer = $1 AND key_id = $2 AND state = $4",
			issuer, keyID, store.StateDisabled, store.StateActive)
	})
}

func execOne(ctx context.Context, tx pgx.Tx, sql string, args ...any) error {
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
