package pgstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

const changesChannel = "iam_changes"

type Listener struct {
	conn *pgx.Conn
	uri  string
}

var _ store.Listener = (*Listener)(nil)

func OpenListener(ctx context.Context, uri string) (*Listener, error) {
	cfg, err := parseConfig(uri)
	if err != nil {
		return nil, err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		return nil, redact.Error(fmt.Errorf("open postgres listener: %w", err), uri)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+changesChannel); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, redact.Error(fmt.Errorf("listen %s: %w", changesChannel, err), uri)
	}
	return &Listener{conn: conn, uri: uri}, nil
}

func (l *Listener) Wait(ctx context.Context) (store.Notice, error) {
	n, err := l.conn.WaitForNotification(ctx)
	switch {
	case err == nil:
		return noticeOf(n.Payload), nil
	case !l.conn.IsClosed() && ctx.Err() != nil:
		return store.NoticeNone, ctx.Err()
	default:
		return store.NoticeNone, fmt.Errorf("%w: %w", store.ErrListenerBroken, redact.Error(err, l.uri))
	}
}

func (l *Listener) Close(ctx context.Context) error {
	return redact.Error(l.conn.Close(ctx), l.uri)
}

func noticeOf(payload string) store.Notice {
	switch payload {
	case "dir":
		return store.NoticeDirectory
	case "rev":
		return store.NoticeRevocations
	default:
		return store.NoticeOther
	}
}
