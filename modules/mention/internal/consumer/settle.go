package consumer

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivannguyendev/chatim/modules/mention/internal/plan"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

var errInvalidEvent = errors.New("invalid mention event")

func errInvalid(err error) error { return fmt.Errorf("%w: %w", errInvalidEvent, err) }

func gone(err error) bool { return errors.Is(err, apperr.ErrNotFound) }

func invalid(err error) bool {
	return errors.Is(err, errInvalidEvent) || errors.Is(err, plan.ErrInvalidEvent)
}

func (c *Consumer) settle(ctx context.Context, d jsconsume.Delivery, err error) {
	switch {
	case err == nil:
		c.report(ctx, d.Ack())
	case gone(err):
		c.dropped.Add(1)
		c.report(ctx, d.Ack())
	case invalid(err):
		c.dropped.Add(1)
		c.report(ctx, d.Term())
	case ctx.Err() != nil:
		c.report(ctx, d.Nak(0))
	default:
		c.failures.Add(1)
		c.handles.Warn(ctx, handleFailedMsg, "err", err)
		c.report(ctx, d.Nak(jsconsume.NakDelay(d.Delivered())))
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
