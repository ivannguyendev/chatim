package projection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const StuckDeliveries uint64 = 6

const (
	fetchFailedMsg  = "projection fetch failed; retrying"
	floorFailedMsg  = "projection could not read its ack floor; retrying"
	handleFailedMsg = "projection failed an event; it comes back later"
	settleFailedMsg = "projection ack or nak failed; the event comes back after the ack wait"
	garbageMsg      = "projection dropped an undecodable event"
	gapMsg          = "projection holds acks above a missing delivery until the server redelivers it"
)

var errStarted = errors.New("projection consumer already started")

type Projection interface {
	Name() string
	Keeps(ev Event) bool
	Handle(ctx context.Context, evs []Event) []error
}

type Source interface {
	eventkit.Source
	AckFloor(ctx context.Context) (uint64, error)
}

type Consumer struct {
	handlers []Projection
	src      Source
	cfg      Config
	fails    *eventkit.LimitedLog
	handles  *eventkit.LimitedLog
	garbage  *eventkit.LimitedLog
	gaps     *eventkit.LimitedLog
	failures atomic.Uint64
	dropped  atomic.Uint64
	lag      atomic.Int64
	stuck    atomic.Uint64
	log      *slog.Logger
	lead     leader
	floor    ackFloor
	started  atomic.Bool
	stopped  context.Context
	halt     context.CancelFunc
	done     chan struct{}
}

func NewConsumer(handlers []Projection, src Source, cfg Config, log *slog.Logger, opts ...Option) (*Consumer, error) {
	if len(handlers) == 0 || src == nil {
		return nil, fmt.Errorf("%w: projection consumer needs handlers and a source", apperr.ErrInvalidArgument)
	}
	for _, h := range handlers {
		if h == nil {
			return nil, fmt.Errorf("%w: projection consumer got a nil handler", apperr.ErrInvalidArgument)
		}
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	log = log.With("projection", DurableName)
	c := &Consumer{
		handlers: handlers, src: src, cfg: cfg, fails: eventkit.NewLimitedLog(log), handles: eventkit.NewLimitedLog(log),
		garbage: eventkit.NewLimitedLog(log), gaps: eventkit.NewLimitedLog(log), log: log, done: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}
	if err := c.lead.validate(); err != nil {
		return nil, err
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
	c.forgetFloor()
	if c.stopped.Err() != nil {
		return nil
	}
	return ctx.Err()
}

func (c *Consumer) step(ctx, fetchCtx context.Context) {
	if !c.leading(ctx) {
		c.forgetFloor()
		c.lag.Store(0)
		c.pause(ctx, c.lead.tick)
		return
	}
	if err := c.loadFloor(fetchCtx); err != nil {
		if c.live(ctx) {
			c.fails.Warn(ctx, floorFailedMsg, "err", err)
		}
		c.pause(ctx, c.cfg.FetchWait)
		return
	}
	ds, err := c.src.Fetch(fetchCtx, c.cfg.FetchBatch, c.cfg.FetchWait)
	if len(ds) == 0 && err == nil {
		c.lag.Store(0)
	}
	if len(ds) > 0 && !c.leading(ctx) {
		c.release(ds)
		return
	}
	c.release(c.handle(ctx, ds))
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

func (c *Consumer) Lag() time.Duration { return time.Duration(c.lag.Load()) }

func (c *Consumer) Stuck() uint64 { return c.stuck.Load() }

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
