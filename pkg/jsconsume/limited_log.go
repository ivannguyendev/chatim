package jsconsume

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type LimitedLog struct {
	log  *slog.Logger
	mu   sync.Mutex
	last time.Time
}

func NewLimitedLog(log *slog.Logger) *LimitedLog {
	return &LimitedLog{log: log}
}

func (l *LimitedLog) Warn(ctx context.Context, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := time.Now(); l.last.IsZero() || now.Sub(l.last) >= time.Second {
		l.last = now
		l.log.WarnContext(ctx, msg, args...)
	}
}
