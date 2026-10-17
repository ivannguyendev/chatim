package timer

import (
	"cmp"
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
	DefaultFetchBatch = 64
	DefaultFetchWait  = 500 * time.Millisecond
	fetchFailedMsg    = "due timer fetch failed; retrying"
	repairFailedMsg   = "count repair failed; the timer comes back later"
	settleFailedMsg   = "due timer ack or nak failed; it comes back after the ack wait"
)

var errStarted = errors.New("timer consumer already started")

type Repairer interface {
	Repair(ctx context.Context, c Check) error
}

type ConsumerDeps struct {
	Source   eventkit.Source
	Members  Repairer
	Messages Repairer
}

type ConsumerConfig struct {
	FetchBatch int
	FetchWait  time.Duration
}

func (c ConsumerConfig) StopTimeout() time.Duration {
	return cmp.Or(c.FetchWait, DefaultFetchWait) + eventkit.FetchExpiryGrace
}

type Consumer struct {
	deps     ConsumerDeps
	cfg      ConsumerConfig
	fails    *eventkit.LimitedLog
	repairs  *eventkit.LimitedLog
	failures atomic.Uint64
	dropped  atomic.Uint64
	started  atomic.Bool
	stopped  context.Context
	halt     context.CancelFunc
	done     chan struct{}
}

func NewConsumer(deps ConsumerDeps, cfg ConsumerConfig, log *slog.Logger) (*Consumer, error) {
	if deps.Source == nil || deps.Members == nil || deps.Messages == nil {
		return nil, fmt.Errorf("%w: timer consumer needs a source, a member repair and a message repair", apperr.ErrInvalidArgument)
	}
	cfg.FetchBatch = cmp.Or(cfg.FetchBatch, DefaultFetchBatch)
	cfg.FetchWait = cmp.Or(cfg.FetchWait, DefaultFetchWait)
	if cfg.FetchBatch <= 0 || cfg.FetchBatch > MaxAckPending || cfg.FetchWait <= 0 {
		return nil, fmt.Errorf("%w: timer consumer fetch batch %d must be 1..%d and fetch wait %v positive", apperr.ErrInvalidArgument, cfg.FetchBatch, MaxAckPending, cfg.FetchWait)
	}
	if log == nil {
		log = slog.Default()
	}
	stopped, halt := context.WithCancel(context.Background())
	return &Consumer{deps: deps, cfg: cfg, fails: eventkit.NewLimitedLog(log), repairs: eventkit.NewLimitedLog(log), stopped: stopped, halt: halt, done: make(chan struct{})}, nil
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
		ds, err := c.deps.Source.Fetch(fetchCtx, c.cfg.FetchBatch, c.cfg.FetchWait)
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

func (c *Consumer) Failures() uint64 { return c.failures.Load() }

func (c *Consumer) Dropped() uint64 { return c.dropped.Load() }

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
