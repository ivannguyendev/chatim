package timer

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
)

func (c *Consumer) handle(ctx context.Context, ds []Delivery) []Delivery {
	for i, d := range ds {
		if !c.live(ctx) {
			return ds[i:]
		}
		c.settle(ctx, d)
	}
	return nil
}

func (c *Consumer) settle(ctx context.Context, d Delivery) {
	chk, err := Decode(d.Data())
	if err != nil {
		c.dropped.Add(1)
		c.report(ctx, d.Term())
		return
	}
	err = c.repairerOf(chk).Repair(ctx, chk)
	switch {
	case err == nil:
		c.report(ctx, d.Ack())
	case eventkit.Gone(err):
		c.dropped.Add(1)
		c.report(ctx, d.Ack())
	case ctx.Err() != nil:
		c.report(ctx, d.Nak(0))
	default:
		c.failures.Add(1)
		c.repairs.Warn(ctx, repairFailedMsg, "check", chk.ID(), "err", err)
		c.report(ctx, d.Nak(eventkit.NakDelay(d.Delivered())))
	}
}

func (c *Consumer) repairerOf(chk Check) Repairer {
	if chk.Counter == MembersCounter {
		return c.deps.Members
	}
	return c.deps.Messages
}

func (c *Consumer) release(ds []Delivery) {
	for _, d := range ds {
		c.report(context.Background(), d.Nak(0))
	}
}

func (c *Consumer) report(ctx context.Context, err error) {
	if err != nil {
		c.fails.Warn(ctx, settleFailedMsg, "err", err)
	}
}
