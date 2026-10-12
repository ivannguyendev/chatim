package reconcile

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

func sleep(ctx context.Context, stop <-chan struct{}, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-stop:
		return errStopped
	case <-ctx.Done():
		return ctx.Err()
	}
}

type limitedLog struct {
	log  *slog.Logger
	mu   sync.Mutex
	last time.Time
}

func (l *limitedLog) warn(ctx context.Context, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := time.Now(); l.last.IsZero() || now.Sub(l.last) >= time.Second {
		l.last = now
		l.log.WarnContext(ctx, msg, args...)
	}
}
