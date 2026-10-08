package worktest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
)

var ErrSettled = errors.New("work delivery already settled")

type entry struct {
	rec   work.Record
	ready time.Time
}

type Broker struct {
	mu       sync.Mutex
	queued   map[int][]entry
	inflight map[int]int
	acked    []work.Record
	naked    []work.Record
	grew     chan struct{}
}

func (b *Broker) Publish(partition int, r work.Record) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pushLocked(partition, entry{rec: r, ready: time.Now()})
}

func (b *Broker) Queue(partition int) work.Queue { return &queue{b: b, partition: partition} }

func (b *Broker) Acked() []work.Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.acked)
}

func (b *Broker) Naked() []work.Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.naked)
}

func (b *Broker) Pending(partition int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queued[partition]) + b.inflight[partition]
}

func (b *Broker) initLocked() {
	if b.queued == nil {
		b.queued, b.inflight, b.grew = map[int][]entry{}, map[int]int{}, make(chan struct{})
	}
}

func (b *Broker) pushLocked(partition int, e entry) {
	b.initLocked()
	b.queued[partition] = append(b.queued[partition], e)
	close(b.grew)
	b.grew = make(chan struct{})
}

func (b *Broker) take(partition, limit int) ([]work.Delivery, time.Time, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.initLocked()
	now := time.Now()
	var out []work.Delivery
	var keep []entry
	var next time.Time
	for _, e := range b.queued[partition] {
		switch {
		case len(out) < limit && !e.ready.After(now):
			out = append(out, &delivery{b: b, partition: partition, rec: e.rec})
		case e.ready.After(now) && (next.IsZero() || e.ready.Before(next)):
			next = e.ready
			keep = append(keep, e)
		default:
			keep = append(keep, e)
		}
	}
	b.queued[partition] = keep
	b.inflight[partition] += len(out)
	return out, next, b.grew
}

func (b *Broker) settle(d *delivery, nak bool, delay time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d.settled {
		return ErrSettled
	}
	d.settled = true
	b.inflight[d.partition]--
	if !nak {
		b.acked = append(b.acked, d.rec)
		return nil
	}
	b.naked = append(b.naked, d.rec)
	b.pushLocked(d.partition, entry{rec: d.rec, ready: time.Now().Add(delay)})
	return nil
}

type queue struct {
	b         *Broker
	partition int
}

func (q *queue) Fetch(ctx context.Context, limit int, wait time.Duration) ([]work.Delivery, error) {
	deadline := time.Now().Add(wait)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out, next, grew := q.b.take(q.partition, limit)
		left := time.Until(deadline)
		if len(out) > 0 || left <= 0 {
			return out, nil
		}
		if !next.IsZero() {
			left = min(left, time.Until(next))
		}
		if err := await(ctx, grew, left); err != nil {
			return nil, err
		}
	}
}

func await(ctx context.Context, grew <-chan struct{}, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-grew:
		return nil
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type delivery struct {
	b         *Broker
	partition int
	rec       work.Record
	settled   bool
}

func (d *delivery) Record() work.Record { return d.rec }

func (d *delivery) Ack() error { return d.b.settle(d, false, 0) }

func (d *delivery) Nak(delay time.Duration) error { return d.b.settle(d, true, delay) }
