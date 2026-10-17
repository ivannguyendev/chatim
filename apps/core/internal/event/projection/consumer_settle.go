package projection

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
)

type outcome uint8

const (
	handled outcome = iota
	gone
	undecodable
	cancelled
	failed
)

func (c *Consumer) settle(ctx context.Context, ds []eventkit.Delivery, outcomes []outcome) {
	for i, d := range ds {
		switch outcomes[i] {
		case handled:
			c.report(ctx, d.Ack())
		case gone:
			c.dropped.Add(1)
			c.report(ctx, d.Ack())
		case undecodable:
			c.dropped.Add(1)
			c.report(ctx, d.Term())
		case cancelled:
			c.report(ctx, d.Nak(0))
		case failed:
			c.failures.Add(1)
			c.report(ctx, d.Nak(eventkit.NakDelay(d.Delivered())))
		}
	}
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
