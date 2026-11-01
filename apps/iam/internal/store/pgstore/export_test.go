package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (s *Store) MigrateUpTo(ctx context.Context, version int) ([]string, error) {
	return s.migrate(ctx, true, version)
}

func (s *Store) PoolConns() int32 { return s.pool.Stat().TotalConns() }

func (s *Store) ReleaseUnheldSlot(ctx context.Context, slot int32) (held, left int32, err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return 0, 0, err
	}
	held = s.PoolConns()
	slotRelease(conn, slot)()
	return held, s.PoolConns(), nil
}

func (l *Listener) BackendPID() uint32 { return l.conn.PgConn().PID() }

const LoadPageSQL = loadPageSQL

func (s *Store) RunAdminTx(ctx context.Context, treeTenant string, readOnly bool, fn func(pgx.Tx) error) error {
	return s.adminTx(ctx, adminTxOptions{treeTenant: treeTenant, readOnly: readOnly}, fn)
}
