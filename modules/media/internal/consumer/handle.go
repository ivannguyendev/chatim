package consumer

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/pkg/core/jsconsume"
)

func (c *Consumer) process(ctx context.Context, j *job) error {
	syncCtx, cancel := context.WithTimeout(ctx, c.cfg.SyncTimeout)
	defer cancel()
	return c.deps.Sync.Sync(syncCtx, j.kind, j.ev)
}

func (c *Consumer) settle(ctx context.Context, d jsconsume.Delivery, err error) {
	switch {
	case err == nil, errors.Is(err, attach.ErrDropped):
		c.report(ctx, d.Ack())
	case errors.Is(err, errInvalidEvent):
		c.deps.Stats.Drop(attach.ReasonInvalid)
		c.report(ctx, d.Term())
	case ctx.Err() != nil:
		c.report(ctx, d.Nak(0))
	case errors.Is(err, attach.ErrWaitUpload):
		c.report(ctx, d.Nak(jsconsume.NakDelay(d.Delivered())))
	default:
		if d.Delivered() >= StuckDeliveries {
			c.stuck.Add(1)
		}
		c.handles.Warn(ctx, handleFailedMsg, "err", err)
		c.report(ctx, d.Nak(jsconsume.NakDelay(d.Delivered())))
	}
}

func (c *Consumer) report(ctx context.Context, err error) {
	if err != nil {
		c.settleFailures.Add(1)
		c.fails.Warn(ctx, settleFailedMsg, "err", err)
	}
}
