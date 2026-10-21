package coreroute

import (
	"context"
	"log/slog"
)

type refresher struct {
	refresh func(context.Context) error
	log     *slog.Logger
	wake    chan struct{}
	done    chan struct{}
}

func newRefresher(refresh func(context.Context) error, log *slog.Logger) *refresher {
	return &refresher{refresh: refresh, log: log, wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (r *refresher) trigger() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *refresher) run(ctx context.Context) {
	defer close(r.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		}
		if err := r.refresh(ctx); err != nil && ctx.Err() == nil {
			r.log.DebugContext(ctx, "triggered slot table reload failed", "err", err)
		}
	}
}

func (r *refresher) wait() { <-r.done }
