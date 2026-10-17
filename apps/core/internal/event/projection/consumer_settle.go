package projection

import (
	"context"

	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

type outcome uint8

const (
	handled outcome = iota
	gone
	undecodable
	cancelled
	failed
)

func (c *Consumer) settle(ctx context.Context, ds []jsconsume.Delivery, outcomes []outcome) {
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
			c.report(ctx, d.Nak(jsconsume.NakDelay(d.Delivered())))
		}
	}
}

func (c *Consumer) release(ds []jsconsume.Delivery) {
	for _, d := range ds {
		c.report(context.Background(), d.Nak(0))
	}
}

func (c *Consumer) report(ctx context.Context, err error) {
	if err != nil {
		c.fails.Warn(ctx, settleFailedMsg, "err", err)
	}
}
