package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

func (s *Store) CreateSigningKey(ctx context.Context, k store.SigningKey, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO signing_keys (kid, state, public_key, private_key_sealed, created_at) VALUES ($1, $2, $3, $4, $5)",
			k.ID, store.KeyPending, k.PublicKey, k.PrivateSealed, k.CreatedAt)
		return err
	})
}

func (s *Store) ActivateSigningKey(ctx context.Context, id string, at time.Time, audit store.AuditEvent) error {
	return s.write(ctx, audit, func(tx pgx.Tx) error {
		var state store.KeyState
		if err := tx.QueryRow(ctx, "SELECT state FROM signing_keys WHERE kid = $1 FOR UPDATE", id).Scan(&state); err != nil {
			return err
		}
		if state == store.KeyActive {
			return nil
		}
		if _, err := tx.Exec(ctx, "UPDATE signing_keys SET state = 3, retired_at = $1 WHERE state = 2", at); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE signing_keys SET state = 2, activated_at = $2, retired_at = NULL WHERE kid = $1", id, at)
		return err
	})
}

func (s *Store) SigningKeys(ctx context.Context) ([]store.SigningKey, error) {
	rows, err := s.pool.Query(ctx, "SELECT kid, state, public_key, private_key_sealed, created_at, activated_at, retired_at FROM signing_keys ORDER BY created_at")
	if err != nil {
		return nil, s.mapErr(err)
	}
	keys, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (store.SigningKey, error) {
		var k store.SigningKey
		var activated, retired *time.Time
		err := r.Scan(&k.ID, &k.State, &k.PublicKey, &k.PrivateSealed, &k.CreatedAt, &activated, &retired)
		if activated != nil {
			k.ActivatedAt = *activated
		}
		if retired != nil {
			k.RetiredAt = *retired
		}
		return k, err
	})
	return keys, s.mapErr(err)
}
