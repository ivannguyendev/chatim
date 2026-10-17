package projection_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

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
}

func delivery(t *testing.T, ev *chatimv1.Event, delivered uint64) *fakeDelivery {
	t.Helper()
	return &fakeDelivery{data: encoded(t, ev), delivered: delivered}
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

func (d *fakeDelivery) settled() (acks, terms int, naks []time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.acks, d.terms, slices.Clone(d.naks)
}

func deliveries(ds []*fakeDelivery) []jsconsume.Delivery {
	out := make([]jsconsume.Delivery, len(ds))
	for i, d := range ds {
		out[i] = d
	}
	return out
}

type fakeSource struct {
	mu       sync.Mutex
	batches  [][]jsconsume.Delivery
	onCancel []jsconsume.Delivery
	ready    chan struct{}
	asked    []fetchAsk
}

type fetchAsk struct {
	limit int
	wait  time.Duration
}

func newFakeSource() *fakeSource { return &fakeSource{ready: make(chan struct{}, 1)} }

func (s *fakeSource) push(ds ...*fakeDelivery) {
	s.mu.Lock()
	s.batches = append(s.batches, deliveries(ds))
	s.mu.Unlock()
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *fakeSource) handOverOnCancel(ds ...*fakeDelivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCancel = deliveries(ds)
}

func (s *fakeSource) Fetch(ctx context.Context, limit int, wait time.Duration) ([]jsconsume.Delivery, error) {
	s.mu.Lock()
	s.asked = append(s.asked, fetchAsk{limit, wait})
	s.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	for {
		if b, ok := s.next(); ok {
			return b, nil
		}
		select {
		case <-s.ready:
		case <-t.C:
			return nil, nil
		case <-ctx.Done():
			s.mu.Lock()
			defer s.mu.Unlock()
			b := s.onCancel
			s.onCancel = nil
			return b, ctx.Err()
		}
	}
}

func (s *fakeSource) next() ([]jsconsume.Delivery, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.batches) == 0 {
		return nil, false
	}
	b := s.batches[0]
	s.batches = s.batches[1:]
	return b, true
}

func (s *fakeSource) firstAsk() (fetchAsk, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.asked) == 0 {
		return fetchAsk{}, false
	}
	return s.asked[0], true
}
