package readiness

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const maxCheckTimeout = 5 * time.Second

type Watch struct {
	check    func(ctx context.Context) error
	interval time.Duration
	log      *slog.Logger
	ready    atomic.Bool
	checked  atomic.Bool
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
}

func New(check func(ctx context.Context) error, interval time.Duration, log *slog.Logger) *Watch {
	return &Watch{check: check, interval: interval, log: log}
}

func (w *Watch) Ready() bool { return w.ready.Load() }

func (w *Watch) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done != nil {
		return nil
	}
	ctx, w.cancel = context.WithCancel(context.WithoutCancel(ctx))
	w.done = make(chan struct{})
	go w.loop(ctx)
	return nil
}

func (w *Watch) Stop() {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	if done == nil {
		return
	}
	cancel()
	<-done
}

func (w *Watch) loop(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		w.probe(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Watch) probe(ctx context.Context) {
	checkCtx, cancel := context.WithTimeout(ctx, min(w.interval, maxCheckTimeout))
	err := w.check(checkCtx)
	cancel()
	if ctx.Err() != nil {
		return
	}
	first := !w.checked.Swap(true)
	was := w.ready.Swap(err == nil)
	switch {
	case err == nil && !was:
		w.log.InfoContext(ctx, "media storage ready")
	case err != nil && (was || first):
		w.log.WarnContext(ctx, "media storage not ready", "err", err)
	}
}
