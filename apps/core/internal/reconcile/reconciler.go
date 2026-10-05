package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	historyLostMsg = "change feed history lost; restarting from now, events in the gap are lost for good"
	lagMsg         = "reconciler lags behind the stream duplicate window; republished events may duplicate"
	failedMsg      = "event republish failed; retrying"
)

var (
	errStarted  = errors.New("reconciler already started")
	errStopped  = errors.New("reconciler stopping")
	errLostLead = errors.New("reconciler no longer owns the leader slot")
)

type Owner interface {
	Owns(slot uint16) bool
}

type Marks interface {
	Acked(ctx context.Context, keys []store.MsgKey) ([]bool, error)
}

type RoomReader interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
}

type Deps struct {
	Feed  store.ChangeFeed
	Rooms RoomReader
	Marks Marks
	Owner Owner
	JS    publish.JetStream
}

type Reconciler struct {
	deps    Deps
	cfg     Config
	log     *slog.Logger
	types   *roomTypes
	drops   limitedLog
	lags    limitedLog
	fails   limitedLog
	dropped atomic.Uint64
	stats   counters
	started atomic.Bool
	stop    chan struct{}
	halt    sync.Once
	done    chan struct{}
}

func New(deps Deps, cfg Config, log *slog.Logger) (*Reconciler, error) {
	if deps.Feed == nil || deps.Rooms == nil || deps.Marks == nil || deps.Owner == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: reconciler needs a feed, rooms, marks, an owner and a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		deps: deps, cfg: cfg, log: log,
		types: newRoomTypes(deps.Rooms, cfg.RoomCache),
		drops: limitedLog{log: log}, lags: limitedLog{log: log}, fails: limitedLog{log: log},
		stop: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

func (r *Reconciler) Dropped() uint64 { return r.dropped.Load() }

func (r *Reconciler) Run(ctx context.Context) error {
	if !r.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return nil
		default:
		}
		if r.leading() {
			r.term(ctx)
		}
		switch err := sleep(ctx, r.stop, r.cfg.Poll); {
		case errors.Is(err, errStopped):
			return nil
		case err != nil:
			return err
		}
	}
}

func (r *Reconciler) Close(ctx context.Context) error {
	r.halt.Do(func() { close(r.stop) })
	if !r.started.Load() {
		return nil
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Reconciler) leading() bool { return r.deps.Owner.Owns(LeaderSlot) }

func (r *Reconciler) term(ctx context.Context) {
	cur, err := r.deps.Feed.Open(ctx)
	if err != nil {
		r.ended(ctx, err)
		return
	}
	r.log.InfoContext(ctx, "reconcile term started")
	r.termStarted()
	defer r.termEnded()
	t := newTerm(r, cur)
	err = t.run(ctx)
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.cfg.Drain)
	defer cancel()
	t.settle(settle)
	if cerr := cur.Close(settle); cerr != nil {
		r.log.WarnContext(ctx, "close change feed", "err", cerr)
	}
	r.ended(ctx, err)
}

func (r *Reconciler) ended(ctx context.Context, err error) {
	switch {
	case err == nil || ctx.Err() != nil || errors.Is(err, errStopped) || errors.Is(err, errLostLead):
	case errors.Is(err, store.ErrFeedHistoryLost):
		r.stats.historyLost.Add(1)
		r.log.ErrorContext(ctx, historyLostMsg, "err", err)
		if ferr := r.deps.Feed.Forget(ctx); ferr != nil {
			r.log.WarnContext(ctx, "forget change feed position", "err", ferr)
		}
	case errors.Is(err, store.ErrFeedBusy):
		r.log.InfoContext(ctx, "change feed held by another consumer; retrying")
	default:
		r.log.WarnContext(ctx, "reconcile term ended", "err", err)
	}
}

func (r *Reconciler) drop(ctx context.Context, msg string, err error) {
	r.dropped.Add(1)
	r.drops.warn(ctx, msg, "err", err)
}

func (r *Reconciler) republishFailed(err error) {
	r.fails.warn(context.Background(), failedMsg, "err", err)
}

func (r *Reconciler) watchLag(ctx context.Context, committed time.Time) {
	lag := time.Since(committed)
	r.stats.lag.Store(int64(lag))
	if lag > r.cfg.DuplicateWindow {
		r.lags.warn(ctx, lagMsg, "lag", lag, "window", r.cfg.DuplicateWindow)
	}
}
