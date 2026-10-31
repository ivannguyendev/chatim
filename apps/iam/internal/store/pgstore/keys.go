package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
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
	return s.write(ctx, audit, func(tx pgx.Tx) error { return activateKey(ctx, tx, id, at) })
}

func (s *Store) ActivateDueSigningKey(ctx context.Context, cutoff, at time.Time, audit store.AuditEvent) (string, error) {
	var kid string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, "SELECT kid FROM signing_keys WHERE state = 1 AND created_at <= $1 ORDER BY created_at DESC LIMIT 1 FOR UPDATE", cutoff).Scan(&kid)
		if errors.Is(err, pgx.ErrNoRows) {
			kid = ""
			return nil
		}
		if err != nil {
			return err
		}
		if err := activateKey(ctx, tx, kid, at); err != nil {
			return err
		}
		audit.Detail = map[string]string{"kid": kid}
		return insertAudit(ctx, tx, audit)
	})
	return kid, s.mapErr(err)
}

func activateKey(ctx context.Context, tx pgx.Tx, id string, at time.Time) error {
	var state store.KeyState
	var created time.Time
	if err := tx.QueryRow(ctx, "SELECT state, created_at FROM signing_keys WHERE kid = $1 FOR UPDATE", id).Scan(&state, &created); err != nil {
		return err
	}
	switch state {
	case store.KeyActive:
		return nil
	case store.KeyRetired:
		return fmt.Errorf("%w: signing key %s is retired", apperr.ErrFailedPrecondition, id)
	case store.KeyPending:
	}
	if _, err := tx.Exec(ctx, "UPDATE signing_keys SET state = 3, retired_at = $1 WHERE state = 2 OR (state = 1 AND created_at < $2)", at, created); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "UPDATE signing_keys SET state = 2, activated_at = $2, retired_at = NULL WHERE kid = $1", id, at)
	return err
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
