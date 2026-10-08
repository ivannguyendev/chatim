package effects

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errStarted = errors.New("effect workers already started")

type Workers struct {
	deps      Deps
	cfg       Config
	log       *slog.Logger
	fails     limitedLog
	processed atomic.Uint64
	failed    atomic.Uint64
	lags      []atomic.Int64
	started   atomic.Bool
	stop      chan struct{}
	halt      sync.Once
	done      chan struct{}
}

func New(deps Deps, cfg Config, log *slog.Logger) (*Workers, error) {
	if deps.Queue == nil || deps.Owner == nil || deps.Registry == nil {
		return nil, fmt.Errorf("%w: effect workers need a queue, an owner and a registry", apperr.ErrInvalidArgument)
	}
	if err := deps.Registry.validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Workers{
		deps: deps, cfg: cfg, log: log, fails: limitedLog{log: log},
		lags: make([]atomic.Int64, cfg.Partitions),
		stop: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

func (w *Workers) Run(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(w.done)
	fetchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() {
		select {
		case <-w.stop:
			cancel()
		case <-fetchCtx.Done():
		}
	})
	for p := range w.cfg.Partitions {
		wg.Go(func() { w.partition(ctx, fetchCtx, p) })
	}
	wg.Wait()
	if w.stopping() {
		return nil
	}
	return ctx.Err()
}

func (w *Workers) Close(ctx context.Context) error {
	w.halt.Do(func() { close(w.stop) })
	if !w.started.Load() {
		return nil
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Workers) Stats() Stats {
	var lag int64
	for i := range w.lags {
		lag = max(lag, w.lags[i].Load())
	}
	return Stats{Processed: w.processed.Load(), Failed: w.failed.Load(), Lag: time.Duration(lag)}
}

func (w *Workers) stopping() bool {
	select {
	case <-w.stop:
		return true
	default:
		return false
	}
}
