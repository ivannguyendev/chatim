package effects

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const (
	fetchFailedMsg  = "work fetch failed; retrying"
	settleFailedMsg = "work ack or nak failed; the record comes back after the ack wait"
)

func (w *Workers) partition(ctx, fetchCtx context.Context, p int) {
	q := w.deps.Queue(p)
	slot := slotOf(p)
	for !w.stopping() && ctx.Err() == nil {
		if !w.deps.Owner.Owns(slot) {
			w.lags[p].Store(0)
			w.pause(ctx)
			continue
		}
		ds, err := q.Fetch(fetchCtx, w.cfg.FetchBatch, w.cfg.FetchWait)
		if w.stopping() || ctx.Err() != nil {
			w.release(ctx, ds)
			return
		}
		w.fetchFailed(ctx, p, err)
		if len(ds) == 0 {
			w.lags[p].Store(0)
			if err != nil {
				w.pause(ctx)
			}
			continue
		}
		w.process(ctx, p, ds)
	}
}

func (w *Workers) fetchFailed(ctx context.Context, p int, err error) {
	if err == nil {
		return
	}
	if bad, ok := errors.AsType[work.BadRecordsError](err); ok {
		w.failed.Add(bad.Terminated)
	}
	w.fails.warn(ctx, fetchFailedMsg, "partition", p, "err", err)
}

func (w *Workers) pause(ctx context.Context) {
	t := time.NewTimer(w.cfg.Poll)
	defer t.Stop()
	select {
	case <-t.C:
	case <-w.stop:
	case <-ctx.Done():
	}
}

func (w *Workers) release(ctx context.Context, ds []work.Delivery) {
	for _, d := range ds {
		if err := d.Nak(0); err != nil {
			w.fails.warn(ctx, settleFailedMsg, "err", err)
		}
	}
}

func slotOf(p int) uint16 {
	if p < 0 || p > math.MaxUint16 {
		return 0
	}
	return uint16(p)
}
