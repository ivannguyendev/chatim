package timer_test

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
)

type fakeDelivery struct {
	mu        sync.Mutex
	data      []byte
	delivered uint64
	acks      int
	terms     int
	naks      []time.Duration
}

func delivery(c timer.Check, delivered uint64) *fakeDelivery {
	return &fakeDelivery{data: timer.Encode(c), delivered: delivered}
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

type fakeSource struct {
	mu      sync.Mutex
	batches [][]timer.Delivery
	errs    []error
	fetches int
}

func (s *fakeSource) push(ds ...*fakeDelivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := make([]timer.Delivery, len(ds))
	for i, d := range ds {
		batch[i] = d
	}
	s.batches = append(s.batches, batch)
}

func (s *fakeSource) Fetch(ctx context.Context, _ int, wait time.Duration) ([]timer.Delivery, error) {
	s.mu.Lock()
	s.fetches++
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		s.mu.Unlock()
		return nil, err
	}
	if len(s.batches) > 0 {
		b := s.batches[0]
		s.batches = s.batches[1:]
		s.mu.Unlock()
		return b, nil
	}
	s.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *fakeSource) fetchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetches
}

type fakeRepairer struct {
	mu      sync.Mutex
	seen    []timer.Check
	results map[uint64]error
	hold    chan struct{}
	held    chan struct{}
}

func (f *fakeRepairer) Repair(ctx context.Context, c timer.Check) error {
	f.mu.Lock()
	f.seen = append(f.seen, c)
	err, hold, held := f.results[c.Seq], f.hold, f.held
	f.hold = nil
	f.mu.Unlock()
	if hold != nil {
		close(held)
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeRepairer) checks() []timer.Check {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

func (f *fakeRepairer) holdNext() (held, release chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hold, f.held = make(chan struct{}), make(chan struct{})
	return f.held, f.hold
}
