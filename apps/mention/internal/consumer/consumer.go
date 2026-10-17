package consumer

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/config"
	"github.com/ivannguyendev/chatim/apps/mention/internal/notify"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

const StuckDeliveries uint64 = 6

const (
	fetchFailedMsg  = "mention fetch failed; retrying"
	handleFailedMsg = "mention consumer failed an event; it comes back later"
	settleFailedMsg = "mention ack or nak failed; the event comes back after the ack wait"
	garbageMsg      = "mention consumer dropped an invalid event"
)

var errStarted = errors.New("mention consumer already started")

type Consumer struct {
	cfg      config.Config
	deps     Deps
	stats    *notify.Stats
	gate     *notify.Gate
	pub      *notify.Publisher
	fails    *jsconsume.LimitedLog
	handles  *jsconsume.LimitedLog
	garbage  *jsconsume.LimitedLog
	failures atomic.Uint64
	dropped  atomic.Uint64
	stuck    atomic.Uint64
	lag      atomic.Int64
	started  atomic.Bool
	stopped  context.Context
	halt     context.CancelFunc
	done     chan struct{}
}

func New(cfg config.Config, deps Deps) (*Consumer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := deps.validate(); err != nil {
		return nil, err
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	log := deps.Log.With("consumer", DurableName)
	stats := &notify.Stats{}
	c := &Consumer{
		cfg: cfg.WithDefaults(), deps: deps, stats: stats,
		gate: notify.NewGate(cfg.WithDefaults().NotifyMaxAge, stats), pub: notify.NewPublisher(deps.Notify, stats),
		fails: jsconsume.NewLimitedLog(log), handles: jsconsume.NewLimitedLog(log), garbage: jsconsume.NewLimitedLog(log),
		done: make(chan struct{}),
	}
	c.stopped, c.halt = context.WithCancel(context.Background())
	return c, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	if !c.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(c.done)
	fetchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	unwatch := context.AfterFunc(c.stopped, cancel)
	defer unwatch()
	for c.live(ctx) {
		c.step(ctx, fetchCtx)
	}
	if c.stopped.Err() != nil {
		return nil
	}
	return ctx.Err()
}

func (c *Consumer) step(ctx, fetchCtx context.Context) {
	ds, err := c.deps.Source.Fetch(fetchCtx, c.cfg.FetchBatch, c.cfg.FetchWait)
	if len(ds) == 0 && err == nil {
		c.lag.Store(0)
	}
	c.handle(ctx, ds)
	if err != nil && c.live(ctx) {
		c.fails.Warn(ctx, fetchFailedMsg, "err", err)
		c.pause(ctx, c.cfg.FetchWait)
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
		return ctx.Err()
	}
}

func (c *Consumer) Name() string { return DurableName }

func (c *Consumer) Failures() uint64 { return c.failures.Load() }

func (c *Consumer) Dropped() uint64 { return c.dropped.Load() }

func (c *Consumer) Stuck() uint64 { return c.stuck.Load() }

func (c *Consumer) Lag() time.Duration { return time.Duration(c.lag.Load()) }

func (c *Consumer) Stats() *notify.Stats { return c.stats }

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
