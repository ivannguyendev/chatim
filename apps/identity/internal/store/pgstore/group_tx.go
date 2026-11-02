package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const (
	treeLockWait      = 5 * time.Second
	rollbackWait      = 5 * time.Second
	serialTimeoutsSQL = "SELECT set_config('lock_timeout', '5s', true), set_config('statement_timeout', '5s', true)"
)

type serialTxOptions struct {
	treeTenant string
}

func treeLockIf(needed bool, tenantID string) serialTxOptions {
	if !needed {
		return serialTxOptions{}
	}
	return serialTxOptions{treeTenant: tenantID}
}

func (o serialTxOptions) txOptions() pgx.TxOptions {
	return pgx.TxOptions{IsoLevel: pgx.Serializable}
}

type serialRun struct {
	conn   *pgxpool.Conn
	tenant string
	tx     pgx.Tx
	locked bool
	broken bool
}

func (s *Store) groupTx(ctx context.Context, opts serialTxOptions, fn func(pgx.Tx) error) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return s.mapErr(err)
	}
	run := &serialRun{conn: conn, tenant: opts.treeTenant}
	defer run.finish(ctx)
	return s.mapErr(run.exec(ctx, opts, fn))
}

func (s *Store) groupWrite(ctx context.Context, opts serialTxOptions, audit store.AuditEvent, fn func(pgx.Tx) error) error {
	return s.groupTx(ctx, opts, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		return insertAudit(ctx, tx, audit)
	})
}

func (r *serialRun) exec(ctx context.Context, opts serialTxOptions, fn func(pgx.Tx) error) error {
	if r.tenant != "" {
		if err := r.lockTree(ctx); err != nil {
			return err
		}
	}
	tx, err := r.conn.BeginTx(ctx, opts.txOptions())
	if err != nil {
		return err
	}
	r.tx = tx
	if _, err := tx.Exec(ctx, serialTimeoutsSQL); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *serialRun) lockTree(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, treeLockWait)
	defer cancel()
	if _, err := r.conn.Exec(wait, "SELECT pg_advisory_lock(2, hashtext($1))", r.tenant); err != nil {
		r.broken = true
		return err
	}
	r.locked = true
	return nil
}

func (r *serialRun) finish(ctx context.Context) {
	if r.tx != nil {
		rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackWait)
		_ = r.tx.Rollback(rollback)
		cancel()
	}
	if r.broken || (r.locked && !r.unlockTree(ctx)) {
		destroy(r.conn)
		return
	}
	r.conn.Release()
}

func (r *serialRun) unlockTree(ctx context.Context) bool {
	wait, cancel := context.WithTimeout(context.WithoutCancel(ctx), slotUnlockTimeout)
	defer cancel()
	var unlocked bool
	err := r.conn.QueryRow(wait, "SELECT pg_advisory_unlock(2, hashtext($1))", r.tenant).Scan(&unlocked)
	return err == nil && unlocked
}
