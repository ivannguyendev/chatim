package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

var errConflict = errors.New("row changed concurrently")

type Options struct {
	MaxConns       int32
	RevocationKeep time.Duration
}

type Store struct {
	pool *pgxpool.Pool
	uri  string
	keep time.Duration
}

func Open(ctx context.Context, uri string, opts Options) (*Store, error) {
	if opts.RevocationKeep <= 0 {
		return nil, errors.New("pgstore: revocation keep must be positive")
	}
	cfg, err := pgxpool.ParseConfig(uri)
	if err != nil {
		return nil, redact.Error(fmt.Errorf("parse postgres uri: %w", err), uri)
	}
	cfg.MaxConns = max(opts.MaxConns, 1)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, redact.Error(fmt.Errorf("open postgres pool: %w", err), uri)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, redact.Error(fmt.Errorf("ping postgres: %w", err), uri)
	}
	return &Store{pool: pool, uri: uri, keep: opts.RevocationKeep}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.mapErr(s.pool.Ping(ctx)) }

func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) write(ctx context.Context, audit store.AuditEvent, fn func(pgx.Tx) error) error {
	return s.mapErr(s.inTx(ctx, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		return insertAudit(ctx, tx, audit)
	}))
}

func (s *Store) mapErr(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return apperr.ErrNotFound
	case errors.Is(err, apperr.ErrNotFound), errors.Is(err, apperr.ErrAlreadyExists), errors.Is(err, apperr.ErrFailedPrecondition):
		return err
	case errors.Is(err, errConflict):
		return fmt.Errorf("%w: %w", apperr.ErrUnavailable, err)
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return fmt.Errorf("%w: %s", apperr.ErrAlreadyExists, pgErr.ConstraintName)
	case errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01"):
		return fmt.Errorf("%w: %s", apperr.ErrUnavailable, pgErr.Code)
	default:
		return fmt.Errorf("%w: %w", apperr.ErrUnavailable, redact.Error(err, s.uri))
	}
}

func insertAudit(ctx context.Context, tx pgx.Tx, a store.AuditEvent) error {
	detail := a.Detail
	if detail == nil {
		detail = map[string]string{}
	}
	body, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("encode audit detail: %w", err)
	}
	_, err = tx.Exec(ctx,
		"INSERT INTO audit_events (tenant_id, at, actor, action, account_id, session_id, ip, detail) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
		a.TenantID, a.At, a.Actor, a.Action, nullable(a.AccountID), nullable(a.SessionID), nullable(a.IP), body)
	return err
}

func expectOne(tag pgconn.CommandTag) error {
	if tag.RowsAffected() != 1 {
		return errConflict
	}
	return nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
