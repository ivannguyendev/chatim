package projection

import (
	"context"
	"maps"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
)

type ackFloor struct {
	seq    uint64
	known  bool
	done   map[uint64]eventkit.Delivery
	failed map[uint64]bool
}

func (c *Consumer) forgetFloor() {
	c.release(c.heldAboveFloor())
	c.floor = ackFloor{}
}

func (c *Consumer) loadFloor(ctx context.Context) error {
	if c.floor.known {
		return nil
	}
	seq, err := c.src.AckFloor(ctx)
	if err != nil {
		return err
	}
	c.floor = ackFloor{seq: seq, known: true, done: make(map[uint64]eventkit.Delivery), failed: make(map[uint64]bool)}
	return nil
}

func (c *Consumer) heldAboveFloor() []eventkit.Delivery {
	seqs := slices.Sorted(maps.Keys(c.floor.done))
	out := make([]eventkit.Delivery, len(seqs))
	for i, seq := range seqs {
		out[i] = c.floor.done[seq]
	}
	return out
}

func (c *Consumer) settle(ctx context.Context, ds []eventkit.Delivery, failed []bool) {
	start := c.floor.seq
	var stale eventkit.Delivery
	for i, d := range ds {
		seq := d.StreamSeq()
		switch {
		case seq <= c.floor.seq:
			stale = d
		case failed[i]:
			delete(c.floor.done, seq)
			c.floor.failed[seq] = true
			c.sendBack(ctx, d)
		default:
			delete(c.floor.failed, seq)
			c.floor.done[seq] = d
		}
	}
	top := c.advance()
	if c.gapped() && c.raiseFloor(ctx) {
		top = c.advance()
	}
	switch {
	case top != nil:
		c.report(ctx, top.Ack())
	case c.floor.seq == start && stale != nil:
		c.report(ctx, stale.Ack())
	}
	if c.gapped() {
		c.gaps.Warn(ctx, gapMsg, "floor", c.floor.seq, "held", len(c.floor.done))
	}
}

func (c *Consumer) gapped() bool { return len(c.floor.done) > 0 && !c.floor.failed[c.floor.seq+1] }

func (c *Consumer) advance() eventkit.Delivery {
	var top eventkit.Delivery
	for {
		d, ok := c.floor.done[c.floor.seq+1]
		if !ok {
			return top
		}
		delete(c.floor.done, c.floor.seq+1)
		c.floor.seq++
		top = d
	}
}

func (c *Consumer) raiseFloor(ctx context.Context) bool {
	seq, err := c.src.AckFloor(ctx)
	if err != nil {
		c.fails.Warn(ctx, floorFailedMsg, "err", err)
		return false
	}
	if seq <= c.floor.seq {
		return false
	}
	c.floor.seq = seq
	maps.DeleteFunc(c.floor.done, func(s uint64, _ eventkit.Delivery) bool { return s <= seq })
	maps.DeleteFunc(c.floor.failed, func(s uint64, _ bool) bool { return s <= seq })
	return true
}

func (c *Consumer) sendBack(ctx context.Context, d eventkit.Delivery) {
	delay := eventkit.NakDelay(d.Delivered())
	if ctx.Err() != nil {
		delay = 0
	}
	c.report(ctx, d.Nak(delay))
}

func (c *Consumer) release(ds []eventkit.Delivery) {
	for _, d := range ds {
		c.report(context.Background(), d.Nak(0))
	}
}

func (c *Consumer) report(ctx context.Context, err error) {
	if err != nil {
		c.fails.Warn(ctx, settleFailedMsg, "err", err)
	}
}
