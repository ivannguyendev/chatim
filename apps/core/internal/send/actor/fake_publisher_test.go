package actor_test

import (
	"sync"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type nopPublisher struct{}

func (nopPublisher) Enqueue(uint64, []*chatimv1.Event) error { return nil }

type publishSpy struct {
	mu      sync.Mutex
	batches map[uint64][][]*chatimv1.Event
}

func (p *publishSpy) Enqueue(room uint64, events []*chatimv1.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.batches == nil {
		p.batches = map[uint64][][]*chatimv1.Event{}
	}
	p.batches[room] = append(p.batches[room], events)
	return nil
}

func (p *publishSpy) events(room uint64) []*chatimv1.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*chatimv1.Event
	for _, b := range p.batches[room] {
		out = append(out, b...)
	}
	return out
}
