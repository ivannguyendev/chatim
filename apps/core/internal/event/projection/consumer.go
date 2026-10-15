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

const (
	fetchFailedMsg  = "projection fetch failed; retrying"
	handleFailedMsg = "projection failed an event; it comes back later"
	settleFailedMsg = "projection ack or nak failed; the event comes back after the ack wait"
)

var errStarted = errors.New("projection consumer already started")

type Projection interface {
	Name() string
	Subjects(root string) []string
	Handle(ctx context.Context, evs []Event) []error
}

type Consumer struct {
	p           Projection
	src         eventkit.Source
	cfg         Config
	fails       *eventkit.LimitedLog
	handles     *eventkit.LimitedLog
	failures    atomic.Uint64
	dropped     atomic.Uint64
	lag         atomic.Int64
	redelivered atomic.Uint64
	started     atomic.Bool
	stopped     context.Context
	halt        context.CancelFunc
	done        chan struct{}
}

func NewConsumer(p Projection, src eventkit.Source, cfg Config, log *slog.Logger) (*Consumer, error) {
	if p == nil || src == nil {
		return nil, fmt.Errorf("%w: projection consumer needs a projection and a source", apperr.ErrInvalidArgument)
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	log = log.With("projection", p.Name())
	stopped, halt := context.WithCancel(context.Background())
	return &Consumer{
		p: p, src: src, cfg: cfg, fails: eventkit.NewLimitedLog(log), handles: eventkit.NewLimitedLog(log),
		stopped: stopped, halt: halt, done: make(chan struct{}),
	}, nil
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
		ds, err := c.src.Fetch(fetchCtx, c.cfg.FetchBatch, c.cfg.FetchWait)
		if len(ds) == 0 && err == nil {
			c.lag.Store(0)
		}
		c.release(c.handle(ctx, ds))
		if err != nil && c.live(ctx) {
			c.fails.Warn(ctx, fetchFailedMsg, "err", err)
			c.pause(ctx)
		}
	}
	if c.stopped.Err() != nil {
		return nil
	}
	return ctx.Err()
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

func (c *Consumer) Name() string { return c.p.Name() }

func (c *Consumer) Failures() uint64 { return c.failures.Load() }

func (c *Consumer) Dropped() uint64 { return c.dropped.Load() }

func (c *Consumer) Lag() time.Duration { return time.Duration(c.lag.Load()) }

func (c *Consumer) RedeliveredMax() uint64 { return c.redelivered.Load() }

func (c *Consumer) live(ctx context.Context) bool { return c.stopped.Err() == nil && ctx.Err() == nil }

func (c *Consumer) pause(ctx context.Context) {
	t := time.NewTimer(c.cfg.FetchWait)
	defer t.Stop()
	select {
	case <-t.C:
	case <-c.stopped.Done():
	case <-ctx.Done():
	}
}
