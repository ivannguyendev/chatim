package consumer

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/pkg/core/jsconsume"
)

func (c *Consumer) consume(pull, work context.Context) {
	s, err := c.deps.Source.Open(pull)
	if err != nil {
		c.fails.Warn(pull, openFailedMsg, "err", err)
		c.pause(pull, IdleWait)
		return
	}
	defer s.Stop()
	draining := false
	for {
		halted := pull.Err() != nil
		if !draining && (halted || !c.deps.Ready()) {
			s.Drain()
			draining = true
		}
		if !halted && !c.acquire(pull) {
			continue
		}
		wait := pull
		if halted {
			wait = work
		}
		d, err := c.receive(wait, s)
		c.take(work, d, halted)
		if c.ended(pull, work, draining, err) {
			return
		}
	}
}

func (c *Consumer) ended(pull, work context.Context, draining bool, err error) bool {
	closed := errors.Is(err, jsconsume.ErrStreamClosed)
	switch {
	case err == nil:
		return false
	case work.Err() != nil, closed && draining:
		return true
	case pull.Err() != nil && !closed:
		return false
	}
	msg := nextFailedMsg
	if closed {
		msg = closedMsg
	}
	c.fails.Warn(pull, msg, "err", err)
	c.pause(pull, IdleWait)
	return closed
}

func (c *Consumer) receive(ctx context.Context, s jsconsume.Stream) (jsconsume.Delivery, error) {
	wait, cancel := context.WithTimeout(ctx, IdleWait)
	defer cancel()
	d, err := s.Next(wait)
	if err != nil && ctx.Err() == nil && wait.Err() != nil {
		c.lastAt.Store(0)
		return nil, nil
	}
	return d, err
}

func (c *Consumer) take(ctx context.Context, d jsconsume.Delivery, halted bool) {
	switch {
	case d == nil && !halted:
		c.release()
	case d == nil:
	case halted:
		c.report(ctx, d.Nak(0))
	default:
		c.start(ctx, d)
	}
}

func (c *Consumer) start(ctx context.Context, d jsconsume.Delivery) {
	kind, ev, at, err := decode(d.Data())
	if err != nil {
		c.garbage.Warn(ctx, garbageMsg, "bytes", len(d.Data()), "err", err)
		c.settle(ctx, d, err)
		c.release()
		return
	}
	c.lastAt.Store(at.UnixNano())
	c.enqueue(ctx, &job{d: c.track(ctx, d), kind: kind, ev: ev})
}

func (c *Consumer) acquire(ctx context.Context) bool {
	select {
	case c.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *Consumer) release() { <-c.slots }
