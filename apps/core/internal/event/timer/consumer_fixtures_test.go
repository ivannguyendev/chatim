package timer_test

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
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
func (d *fakeDelivery) InProgress() error { return nil }

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
	batches [][]jsconsume.Delivery
	errs    []error
	fetches int
	ready   chan struct{}
}

func (s *fakeSource) push(ds ...*fakeDelivery) {
	batch := make([]jsconsume.Delivery, len(ds))
	for i, d := range ds {
		batch[i] = d
	}
	s.mu.Lock()
	s.batches = append(s.batches, batch)
	ready := s.readyLocked()
	s.mu.Unlock()
	select {
	case ready <- struct{}{}:
	default:
	}
}

func (s *fakeSource) Fetch(ctx context.Context, _ int, wait time.Duration) ([]jsconsume.Delivery, error) {
	t := time.NewTimer(wait)
	defer t.Stop()
	s.mu.Lock()
	s.fetches++
	s.mu.Unlock()
	for {
		b, ok, ready, err := s.next()
		if ok {
			return b, err
		}
		select {
		case <-ready:
		case <-t.C:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (s *fakeSource) next() ([]jsconsume.Delivery, bool, chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return nil, true, nil, err
	}
	if len(s.batches) > 0 {
		b := s.batches[0]
		s.batches = s.batches[1:]
		return b, true, nil, nil
	}
	return nil, false, s.readyLocked(), nil
}

func (s *fakeSource) readyLocked() chan struct{} {
	if s.ready == nil {
		s.ready = make(chan struct{}, 1)
	}
	return s.ready
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
