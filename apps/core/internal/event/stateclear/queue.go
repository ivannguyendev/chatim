package stateclear

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

var errStarted = errors.New("state clear queue already started")

type Clearer interface {
	Clear(ctx context.Context, targets []store.StateTarget) error
}

type Queue struct {
	store    Clearer
	cfg      Config
	log      *slog.Logger
	fails    *jsconsume.LimitedLog
	full     *jsconsume.LimitedLog
	in       chan store.StateTarget
	mu       sync.RWMutex
	closed   bool
	started  atomic.Bool
	abort    context.Context
	halt     context.CancelFunc
	done     chan struct{}
	cleared  atomic.Uint64
	dropped  atomic.Uint64
	failures atomic.Uint64
}

func New(st Clearer, cfg Config, log *slog.Logger) (*Queue, error) {
	if st == nil {
		return nil, fmt.Errorf("%w: state clear queue needs a store", apperr.ErrInvalidArgument)
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	abort, halt := context.WithCancel(context.Background())
	return &Queue{
		store: st, cfg: cfg, log: log, fails: jsconsume.NewLimitedLog(log), full: jsconsume.NewLimitedLog(log),
		in: make(chan store.StateTarget, cfg.Queue), abort: abort, halt: halt, done: make(chan struct{}),
	}, nil
}

func (q *Queue) Add(t store.StateTarget) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		q.dropped.Add(1)
		return
	}
	select {
	case q.in <- t:
	default:
		q.dropped.Add(1)
		q.full.Warn(context.Background(), "pending state clear queue full; dropping clears", "capacity", q.cfg.Queue)
	}
}

func (q *Queue) AddAll(targets []store.StateTarget) {
	if len(targets) == 0 {
		return
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		q.dropped.Add(uint64(len(targets)))
		return
	}
	for i, t := range targets {
		select {
		case q.in <- t:
		default:
			q.dropped.Add(uint64(len(targets[i:])))
			q.full.Warn(context.Background(), "pending state clear queue full; dropping clears", "capacity", q.cfg.Queue)
			return
		}
	}
}

func (q *Queue) Close(ctx context.Context) error {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.in)
	}
	q.mu.Unlock()
	defer q.discardQueued()
	if !q.started.Load() {
		return nil
	}
	drain, cancel := context.WithTimeout(ctx, DrainTimeout)
	defer cancel()
	select {
	case <-q.done:
		return nil
	case <-drain.Done():
		q.halt()
		<-q.done
		return ctx.Err()
	}
}

func (q *Queue) discardQueued() {
	for range q.in {
		q.dropped.Add(1)
	}
}

func (q *Queue) Cleared() uint64 { return q.cleared.Load() }

func (q *Queue) Dropped() uint64 { return q.dropped.Load() }

func (q *Queue) Failures() uint64 { return q.failures.Load() }
