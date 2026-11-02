package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

var retryLaterCodes = []string{"40001", "40P01", "55P03", "57014"}

var domainErrors = []error{
	apperr.ErrNotFound, apperr.ErrAlreadyExists, apperr.ErrFailedPrecondition, apperr.ErrInvalidArgument,
	apperr.ErrPermissionDenied, apperr.ErrUnauthenticated, apperr.ErrUnavailable,
}

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
	cfg, err := parseConfig(uri)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = max(opts.MaxConns, 1)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, redact.Error(fmt.Errorf("open postgres pool: %w", err), uri)
	}
	return &Store{pool: pool, uri: uri, keep: opts.RevocationKeep}, nil
}

func parseConfig(uri string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(uri)
	if err != nil {
		return nil, redact.Error(fmt.Errorf("parse postgres uri: %w", err), uri)
	}
	return cfg, nil
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
	case isDomainError(err):
		return err
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return fmt.Errorf("%w: %s", apperr.ErrAlreadyExists, pgErr.ConstraintName)
	case errors.As(err, &pgErr) && pgErr.Code == "23503":
		return fmt.Errorf("%w: %s", apperr.ErrNotFound, pgErr.ConstraintName)
	case errors.As(err, &pgErr) && pgErr.Code == "23514" && pgErr.ConstraintName == "groups_no_cycle":
		return errGroupCycle
	case errors.As(err, &pgErr) && slices.Contains(retryLaterCodes, pgErr.Code):
		return fmt.Errorf("%w: %s", apperr.ErrUnavailable, pgErr.Code)
	default:
		return fmt.Errorf("%w: %w", apperr.ErrUnavailable, redact.Error(err, s.uri))
	}
}

func isDomainError(err error) bool {
	return slices.ContainsFunc(domainErrors, func(target error) bool { return errors.Is(err, target) })
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
		"INSERT INTO audit_events (tenant_id, at, actor, action, user_id, detail) VALUES ($1, $2, $3, $4, $5, $6)",
		a.TenantID, a.At, a.Actor, a.Action, nullable(a.UserID), body)
	return err
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
