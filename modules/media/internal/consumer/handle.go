package consumer

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/pkg/core/jsconsume"
)

func (c *Consumer) handle(ctx context.Context, ds []jsconsume.Delivery) {
	if len(ds) == 0 {
		return
	}
	if !c.live(ctx) {
		c.release(ds)
		return
	}
	batch := track(ds)
	defer c.keepAlive(ctx, batch)()
	c.work(ctx, c.prepare(ctx, batch))
}

func (c *Consumer) prepare(ctx context.Context, batch []*tracked) []job {
	jobs := make([]job, 0, len(batch))
	var oldest time.Time
	for _, d := range batch {
		kind, ev, at, err := decode(d.Data())
		if err != nil {
			c.garbage.Warn(ctx, garbageMsg, "bytes", len(d.Data()), "err", err)
			c.settle(ctx, d, err)
			continue
		}
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
		jobs = append(jobs, job{d: d, kind: kind, ev: ev})
	}
	if !oldest.IsZero() {
		c.lag.Store(int64(max(time.Since(oldest), 0)))
	}
	return jobs
}

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

func (c *Consumer) release(ds []jsconsume.Delivery) {
	for _, d := range ds {
		c.report(context.Background(), d.Nak(0))
	}
}

func (c *Consumer) report(ctx context.Context, err error) {
	if err != nil {
		c.settleFailures.Add(1)
		c.fails.Warn(ctx, settleFailedMsg, "err", err)
	}
}
