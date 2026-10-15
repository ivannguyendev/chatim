package projection

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
)

var errUnanswered = errors.New("projection gave no result for the event")

func (c *Consumer) handle(ctx context.Context, ds []eventkit.Delivery) []eventkit.Delivery {
	if len(ds) == 0 || !c.live(ctx) {
		return ds
	}
	evs, held := c.decode(ctx, ds)
	if len(evs) == 0 {
		return nil
	}
	c.observe(evs, held)
	errs := c.p.Handle(ctx, evs)
	for i, d := range held {
		c.settle(ctx, d, evs[i], resultOf(errs, i, len(evs)))
	}
	return nil
}

func (c *Consumer) decode(ctx context.Context, ds []eventkit.Delivery) ([]Event, []eventkit.Delivery) {
	evs := make([]Event, 0, len(ds))
	held := make([]eventkit.Delivery, 0, len(ds))
	for _, d := range ds {
		ev, err := Decode(d.Data())
		if err != nil {
			c.dropped.Add(1)
			c.report(ctx, d.Term())
			continue
		}
		evs = append(evs, ev)
		held = append(held, d)
	}
	return evs, held
}

func (c *Consumer) observe(evs []Event, held []eventkit.Delivery) {
	oldest := evs[0].At
	var most uint64
	for i, ev := range evs {
		if ev.At.Before(oldest) {
			oldest = ev.At
		}
		most = max(most, held[i].Delivered())
	}
	c.lag.Store(int64(max(time.Since(oldest), 0)))
	c.redelivered.Store(most)
}

func (c *Consumer) settle(ctx context.Context, d eventkit.Delivery, ev Event, err error) {
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
		c.handles.Warn(ctx, handleFailedMsg, "event", ev.Proto.GetId(), "err", err)
		c.report(ctx, d.Nak(eventkit.NakDelay(d.Delivered())))
	}
}

func resultOf(errs []error, i, want int) error {
	if len(errs) != want {
		return errUnanswered
	}
	return errs[i]
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
