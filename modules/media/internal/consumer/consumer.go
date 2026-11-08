package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/core/jsconsume"
)

const (
	fetchFailedMsg  = "media fetch failed; retrying"
	handleFailedMsg = "media consumer failed an event; it comes back later"
	settleFailedMsg = "media ack or nak failed; the event comes back after the ack wait"
	garbageMsg      = "media consumer dropped an invalid event"
	progressMsg     = "media in progress signal failed; the event may come back after the ack wait"
)

var errStarted = errors.New("media consumer already started")

type Syncer interface {
	Sync(ctx context.Context, kind attach.Kind, ev attach.Event) error
}

type Deps struct {
	Source jsconsume.Source
	Sync   Syncer
	Stats  *attach.Stats
	Ready  func() bool
	Log    *slog.Logger
}

type Consumer struct {
	cfg            Config
	deps           Deps
	fails          *jsconsume.LimitedLog
	handles        *jsconsume.LimitedLog
	garbage        *jsconsume.LimitedLog
	stuck          atomic.Uint64
	settleFailures atomic.Uint64
	lag            atomic.Int64
	started        atomic.Bool
	stopped        context.Context
	halt           context.CancelFunc
	aborted        context.Context
	abort          context.CancelFunc
	done           chan struct{}
}

func New(cfg Config, deps Deps) (*Consumer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps.Source == nil || deps.Sync == nil || deps.Stats == nil || deps.Ready == nil {
		return nil, fmt.Errorf("%w: media consumer needs a source, a syncer, stats and a readiness check", apperr.ErrInvalidArgument)
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	log := deps.Log.With("consumer", DurableName)
	c := &Consumer{
		cfg: cfg, deps: deps, done: make(chan struct{}),
		fails: jsconsume.NewLimitedLog(log), handles: jsconsume.NewLimitedLog(log), garbage: jsconsume.NewLimitedLog(log),
	}
	c.stopped, c.halt = context.WithCancel(context.Background())
	c.aborted, c.abort = context.WithCancel(context.Background())
	return c, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	if !c.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(c.done)
	workCtx, cancelWork := context.WithCancel(ctx)
	defer cancelWork()
	unabort := context.AfterFunc(c.aborted, cancelWork)
	defer unabort()
	fetchCtx, cancel := context.WithCancel(workCtx)
	defer cancel()
	unwatch := context.AfterFunc(c.stopped, cancel)
	defer unwatch()
	for c.live(workCtx) {
		c.step(workCtx, fetchCtx)
	}
	if c.stopped.Err() != nil {
		return nil
	}
	return ctx.Err()
}

func (c *Consumer) step(ctx, fetchCtx context.Context) {
	if !c.deps.Ready() {
		c.pause(ctx, FetchWait)
		return
	}
	ds, err := c.deps.Source.Fetch(fetchCtx, c.cfg.FetchBatch, FetchWait)
	if len(ds) == 0 && err == nil {
		c.lag.Store(0)
	}
	c.handle(ctx, ds)
	if err != nil && c.live(ctx) {
		c.fails.Warn(ctx, fetchFailedMsg, "err", err)
		c.pause(ctx, FetchWait)
	}
}

func (c *Consumer) Close(ctx context.Context) error {
	c.halt()
	if !c.started.Load() {
		return nil
	}
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
	}
	before := c.settleFailures.Load()
	c.abort()
	<-c.done
	if c.settleFailures.Load() != before {
		return ctx.Err()
	}
	return nil
}

func (c *Consumer) Stuck() uint64 { return c.stuck.Load() }

func (c *Consumer) Lag() time.Duration { return time.Duration(c.lag.Load()) }

func (c *Consumer) live(ctx context.Context) bool { return c.stopped.Err() == nil && ctx.Err() == nil }

func (c *Consumer) pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-c.stopped.Done():
	case <-ctx.Done():
	}
}
