package projection

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
)

var errUnanswered = errors.New("projection gave no result for the event")

type batch struct {
	evs    []Event
	at     []int
	failed []bool
	gone   []bool
}

func (c *Consumer) handle(ctx context.Context, ds []eventkit.Delivery) []eventkit.Delivery {
	if len(ds) == 0 || !c.live(ctx) {
		return ds
	}
	b := c.decode(ctx, ds)
	c.observe(b.evs, ds)
	if len(b.evs) > 0 {
		c.dispatch(ctx, b)
	}
	c.settle(ctx, ds, b.failed)
	return nil
}

func (c *Consumer) decode(ctx context.Context, ds []eventkit.Delivery) batch {
	b := batch{evs: make([]Event, 0, len(ds)), at: make([]int, 0, len(ds)), failed: make([]bool, len(ds))}
	for i, d := range ds {
		ev, err := Decode(d.Data())
		if err != nil {
			c.dropped.Add(1)
			c.garbage.Warn(ctx, garbageMsg, "bytes", len(d.Data()), "err", err)
			continue
		}
		b.evs = append(b.evs, ev)
		b.at = append(b.at, i)
	}
	b.gone = make([]bool, len(b.evs))
	return b
}

func (c *Consumer) observe(evs []Event, ds []eventkit.Delivery) {
	for _, d := range ds {
		if d.Delivered() >= StuckDeliveries {
			c.stuck.Add(1)
		}
	}
	if len(evs) == 0 {
		return
	}
	oldest := evs[0].At
	for _, ev := range evs[1:] {
		if ev.At.Before(oldest) {
			oldest = ev.At
		}
	}
	c.lag.Store(int64(max(time.Since(oldest), 0)))
}

func (c *Consumer) dispatch(ctx context.Context, b batch) {
	subsets := make([][]int, len(c.handlers))
	results := make([][]error, len(c.handlers))
	for h, p := range c.handlers {
		for i, ev := range b.evs {
			if p.Keeps(ev) {
				subsets[h] = append(subsets[h], i)
			}
		}
	}
	var wg sync.WaitGroup
	for h, p := range c.handlers {
		if len(subsets[h]) > 0 {
			wg.Go(func() { results[h] = p.Handle(ctx, pick(b.evs, subsets[h])) })
		}
	}
	wg.Wait()
	for h, idx := range subsets {
		for j, i := range idx {
			c.judge(ctx, b, i, c.handlers[h].Name(), resultOf(results[h], j, len(idx)))
		}
	}
	for i, gone := range b.gone {
		if gone && !b.failed[b.at[i]] {
			c.dropped.Add(1)
		}
	}
}

func (c *Consumer) judge(ctx context.Context, b batch, i int, handler string, err error) {
	switch {
	case err == nil:
	case eventkit.Gone(err):
		b.gone[i] = true
	case b.failed[b.at[i]]:
	case ctx.Err() != nil:
		b.failed[b.at[i]] = true
	default:
		b.failed[b.at[i]] = true
		c.failures.Add(1)
		c.handles.Warn(ctx, handleFailedMsg, "handler", handler, "event", b.evs[i].Proto.GetId(), "err", err)
	}
}

func pick(evs []Event, idx []int) []Event {
	out := make([]Event, len(idx))
	for j, i := range idx {
		out[j] = evs[i]
	}
	return out
}

func resultOf(errs []error, i, want int) error {
	if len(errs) != want {
		return errUnanswered
	}
	return errs[i]
}
