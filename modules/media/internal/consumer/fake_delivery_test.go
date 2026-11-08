package consumer_test

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/pkg/core/jsconsume"
)

type fakeDelivery struct {
	mu        sync.Mutex
	data      []byte
	delivered uint64
	acks      int
	terms     int
	naks      []time.Duration
	progress  int
	late      int
}

func (d *fakeDelivery) Data() []byte      { return d.data }
func (d *fakeDelivery) Delivered() uint64 { return d.delivered }

func (d *fakeDelivery) Ack() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.acks++
	return nil
}

func (d *fakeDelivery) Nak(delay time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.naks = append(d.naks, delay)
	return nil
}

func (d *fakeDelivery) Term() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.terms++
	return nil
}

func (d *fakeDelivery) InProgress() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.progress++
	if d.acks+d.terms+len(d.naks) > 0 {
		d.late++
	}
	return nil
}

func (d *fakeDelivery) progressed() (calls, late int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.progress, d.late
}

type settledAs struct {
	acks, terms int
	naks        []time.Duration
}

func settledOf(d *fakeDelivery) settledAs {
	d.mu.Lock()
	defer d.mu.Unlock()
	return settledAs{d.acks, d.terms, slices.Clone(d.naks)}
}

type fakeQueue struct {
	mu    sync.Mutex
	items []jsconsume.Delivery
	asked int
}

func (q *fakeQueue) push(ds ...*fakeDelivery) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, d := range ds {
		q.items = append(q.items, d)
	}
}

func (q *fakeQueue) fetches() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.asked
}

func (q *fakeQueue) Fetch(ctx context.Context, limit int, wait time.Duration) ([]jsconsume.Delivery, error) {
	q.mu.Lock()
	q.asked++
	n := min(limit, len(q.items))
	out := q.items[:n:n]
	q.items = q.items[n:]
	q.mu.Unlock()
	if n > 0 {
		return out, nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
