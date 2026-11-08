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
	ackWait   time.Duration
	touched   time.Time
	expired   bool
}

func (d *fakeDelivery) touch() {
	if d.ackWait > 0 && d.acks+d.terms+len(d.naks) == 0 && time.Since(d.touched) > d.ackWait {
		d.expired = true
	}
	d.touched = time.Now()
}

func (d *fakeDelivery) redelivered() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.acks+d.terms+len(d.naks) == 0 && d.ackWait > 0 && time.Since(d.touched) > d.ackWait {
		return true
	}
	return d.expired
}

func (d *fakeDelivery) Data() []byte      { return d.data }
func (d *fakeDelivery) Delivered() uint64 { return d.delivered }

func (d *fakeDelivery) Ack() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.touch()
	d.acks++
	return nil
}

func (d *fakeDelivery) Nak(delay time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.touch()
	d.naks = append(d.naks, delay)
	return nil
}

func (d *fakeDelivery) Term() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.touch()
	d.terms++
	return nil
}

func (d *fakeDelivery) InProgress() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.touch()
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

type fakeStream struct {
	mu       sync.Mutex
	items    []jsconsume.Delivery
	wake     chan struct{}
	opens    int
	open     bool
	draining bool
	taken    int
	failOpen error
}

func newStream(ds ...*fakeDelivery) *fakeStream {
	s := &fakeStream{wake: make(chan struct{})}
	s.push(ds...)
	return s
}

func (s *fakeStream) signal() {
	close(s.wake)
	s.wake = make(chan struct{})
}

func (s *fakeStream) push(ds ...*fakeDelivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range ds {
		s.items = append(s.items, d)
	}
	s.signal()
}

func (s *fakeStream) stats() (opens, taken, left int, open bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opens, s.taken, len(s.items), s.open
}

func (s *fakeStream) Open(context.Context) (jsconsume.Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opens++
	if s.failOpen != nil {
		return nil, s.failOpen
	}
	s.open, s.draining = true, false
	return s, nil
}

func (s *fakeStream) Next(ctx context.Context) (jsconsume.Delivery, error) {
	for {
		s.mu.Lock()
		if !s.open {
			s.mu.Unlock()
			return nil, jsconsume.ErrStreamClosed
		}
		if len(s.items) > 0 {
			d := s.items[0]
			s.items = s.items[1:]
			s.taken++
			s.mu.Unlock()
			return d, nil
		}
		if s.draining {
			s.open = false
			s.mu.Unlock()
			return nil, jsconsume.ErrStreamClosed
		}
		wake := s.wake
		s.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (s *fakeStream) Drain() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.draining = true
	s.signal()
}

func (s *fakeStream) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open = false
	s.signal()
}
