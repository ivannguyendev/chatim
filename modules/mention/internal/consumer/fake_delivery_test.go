package consumer_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/jsconsume"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
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

func delivery(t *testing.T, ev *chatimv1.Event, delivered uint64) *fakeDelivery {
	t.Helper()
	data, err := proto.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &fakeDelivery{data: data, delivered: delivered}
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

func (d *fakeDelivery) settled() (acks, terms int, naks []time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.acks, d.terms, slices.Clone(d.naks)
}

type fakeQueue struct {
	mu    sync.Mutex
	items []jsconsume.Delivery
	asked []int
}

func (q *fakeQueue) push(ds ...*fakeDelivery) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, d := range ds {
		q.items = append(q.items, d)
	}
}

func (q *fakeQueue) Fetch(ctx context.Context, limit int, wait time.Duration) ([]jsconsume.Delivery, error) {
	q.mu.Lock()
	q.asked = append(q.asked, limit)
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

type expiringSource struct {
	mu   sync.Mutex
	held []jsconsume.Delivery
}

func (s *expiringSource) Fetch(ctx context.Context, _ int, wait time.Duration) ([]jsconsume.Delivery, error) {
	time.Sleep(wait)
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.held
	s.held = nil
	return b, ctx.Err()
}
