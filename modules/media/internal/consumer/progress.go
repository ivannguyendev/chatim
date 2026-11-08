package consumer

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/pkg/core/jsconsume"
)

type tracked struct {
	jsconsume.Delivery
	mu      sync.Mutex
	settled bool
	every   time.Duration
	timer   *time.Timer
	ctx     context.Context
	log     *jsconsume.LimitedLog
}

func (c *Consumer) track(ctx context.Context, d jsconsume.Delivery) *tracked {
	t := &tracked{Delivery: d, every: c.cfg.AckWait / 3, ctx: ctx, log: c.fails}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timer = time.AfterFunc(t.every, t.keepAlive)
	return t
}

func (t *tracked) keepAlive() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.settled {
		return
	}
	if err := t.InProgress(); err != nil {
		t.log.Warn(t.ctx, progressMsg, "err", err)
	}
	t.timer.Reset(t.every)
}

func (t *tracked) Ack() error { return t.finish(t.Delivery.Ack) }

func (t *tracked) Term() error { return t.finish(t.Delivery.Term) }

func (t *tracked) Nak(delay time.Duration) error {
	return t.finish(func() error { return t.Delivery.Nak(delay) })
}

func (t *tracked) finish(settle func() error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.settled = true
	t.timer.Stop()
	return settle()
}
