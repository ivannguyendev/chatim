package pgstore

import (
	"context"
	"maps"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const (
	adminTreeLockWait = 5 * time.Second
	adminRollbackWait = 5 * time.Second
	adminTimeoutsSQL  = "SELECT set_config('lock_timeout', '5s', true), set_config('statement_timeout', '5s', true)"
)

type adminTxOptions struct {
	treeTenant string
	readOnly   bool
}

func treeLockIf(needed bool, tenantID string) adminTxOptions {
	if !needed {
		return adminTxOptions{}
	}
	return adminTxOptions{treeTenant: tenantID}
}

func (o adminTxOptions) txOptions() pgx.TxOptions {
	if o.readOnly {
		return pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	}
	return pgx.TxOptions{IsoLevel: pgx.Serializable}
}

type adminRun struct {
	conn   *pgxpool.Conn
	tenant string
	tx     pgx.Tx
	locked bool
	broken bool
}

func (s *Store) adminTx(ctx context.Context, opts adminTxOptions, fn func(pgx.Tx) error) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return s.mapErr(err)
	}
	run := &adminRun{conn: conn, tenant: opts.treeTenant}
	defer run.finish(ctx)
	return s.mapErr(run.exec(ctx, opts, fn))
}

func (s *Store) adminWrite(ctx context.Context, opts adminTxOptions, audit store.AuditEvent, fn func(pgx.Tx) error) error {
	return s.adminTx(ctx, opts, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		return insertAudit(ctx, tx, audit)
	})
}

func (r *adminRun) exec(ctx context.Context, opts adminTxOptions, fn func(pgx.Tx) error) error {
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
	if _, err := tx.Exec(ctx, adminTimeoutsSQL); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *adminRun) lockTree(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, adminTreeLockWait)
	defer cancel()
	if _, err := r.conn.Exec(wait, "SELECT pg_advisory_lock(2, hashtext($1))", r.tenant); err != nil {
		r.broken = true
		return err
	}
	r.locked = true
	return nil
}

func (r *adminRun) finish(ctx context.Context) {
	if r.tx != nil {
		rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), adminRollbackWait)
		_ = r.tx.Rollback(rollback)
		cancel()
	}
	if r.broken || (r.locked && !r.unlockTree(ctx)) {
		destroy(r.conn)
		return
	}
	r.conn.Release()
}

func (r *adminRun) unlockTree(ctx context.Context) bool {
	wait, cancel := context.WithTimeout(context.WithoutCancel(ctx), slotUnlockTimeout)
	defer cancel()
	var unlocked bool
	err := r.conn.QueryRow(wait, "SELECT pg_advisory_unlock(2, hashtext($1))", r.tenant).Scan(&unlocked)
	return err == nil && unlocked
}

func withAuditDetail(a store.AuditEvent, key, value string) store.AuditEvent {
	detail := maps.Clone(a.Detail)
	if detail == nil {
		detail = map[string]string{}
	}
	detail[key] = value
	a.Detail = detail
	return a
}
