package actor_test

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type nopPublisher struct{}

func (nopPublisher) Enqueue(uint64, []*chatimv1.Event) error { return nil }

func (nopPublisher) Skip(uint64, []uint64) error { return nil }

type nopMarker struct{}

func (nopMarker) MarkActive(context.Context, uint64) error { return nil }

type journal struct {
	mu      sync.Mutex
	entries []string
}

func (j *journal) add(s string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, s)
}

func (j *journal) list() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.entries)
}

type publishSpy struct {
	mu      sync.Mutex
	batches map[uint64][][]*chatimv1.Event
	handed  map[uint64][]string
	err     error
}

func (p *publishSpy) Enqueue(room uint64, events []*chatimv1.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	if p.batches == nil {
		p.batches = map[uint64][][]*chatimv1.Event{}
	}
	p.batches[room] = append(p.batches[room], events)
	pts := make([]uint64, len(events))
	for i, ev := range events {
		pts[i] = ev.GetPts()
	}
	p.note(room, "events", pts)
	return nil
}

func (p *publishSpy) Skip(room uint64, pts []uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.note(room, "skip", pts)
	return nil
}

func (p *publishSpy) note(room uint64, kind string, pts []uint64) {
	if p.handed == nil {
		p.handed = map[uint64][]string{}
	}
	p.handed[room] = append(p.handed[room], fmt.Sprint(kind, pts))
}

func (p *publishSpy) calls(room uint64) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.handed[room])
}

func (p *publishSpy) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
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

type markSpy struct {
	log *journal

	mu    sync.Mutex
	tries map[uint64]int
	err   error
}

func (m *markSpy) MarkActive(_ context.Context, room uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tries == nil {
		m.tries = map[uint64]int{}
	}
	m.tries[room]++
	m.log.add("mark")
	return m.err
}

func (m *markSpy) attempts(room uint64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tries[room]
}

func (m *markSpy) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

type journaledSubmitter struct {
	actor.Submitter
	log *journal
}

func (s journaledSubmitter) Submit(ctx context.Context, g flush.Group) error {
	s.log.add("submit")
	return s.Submitter.Submit(ctx, g)
}
