package clear

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (q *Queue) Run(ctx context.Context) error {
	if !q.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(q.done)
	base, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(q.abort, cancel)
	defer stop()
	timer := time.NewTimer(q.cfg.Flush)
	timer.Stop()
	defer timer.Stop()
	batch := make([]store.StateTarget, 0, q.cfg.Batch)
	for {
		select {
		case t, open := <-q.in:
			if !open {
				q.flush(base, batch)
				return nil
			}
			if len(batch) == 0 {
				timer.Reset(q.cfg.Flush)
			}
			batch = append(batch, t)
			if len(batch) >= q.cfg.Batch {
				timer.Stop()
				batch = q.flush(base, batch)
			}
		case <-timer.C:
			batch = q.flush(base, batch)
		case <-ctx.Done():
			q.dropped.Add(uint64(len(batch)))
			return ctx.Err()
		}
	}
}

func (q *Queue) flush(base context.Context, batch []store.StateTarget) []store.StateTarget {
	n := uint64(len(batch))
	if n == 0 {
		return batch
	}
	defer clear(batch)
	if base.Err() != nil {
		q.dropped.Add(n)
		return batch[:0]
	}
	ctx, cancel := context.WithTimeout(base, ClearTimeout)
	err := q.store.Clear(ctx, batch)
	cancel()
	if err != nil {
		q.failures.Add(n)
		q.fails.warn(base, "pending state clear failed; the sweeper republishes these events", "targets", n, "err", err)
		return batch[:0]
	}
	q.cleared.Add(n)
	return batch[:0]
}
