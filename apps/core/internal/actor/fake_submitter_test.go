package actor_test

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

type outcome func(msgs []domain.Message) []store.Result

type fakeSubmitter struct {
	store *memstore.Messages
	gate  chan struct{}
	once  sync.Once

	mu     sync.Mutex
	groups [][]domain.Message
	script []outcome
	always outcome
	err    error
}

func (s *fakeSubmitter) Submit(ctx context.Context, g flush.Group) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.err != nil {
		defer s.mu.Unlock()
		return s.err
	}
	msgs := slices.Clone(g.Msgs)
	s.groups = append(s.groups, msgs)
	fn := s.insert
	switch {
	case len(s.script) > 0:
		fn, s.script = s.script[0], s.script[1:]
	case s.always != nil:
		fn = s.always
	}
	gate := s.gate
	s.mu.Unlock()
	if gate == nil {
		g.Done(fn(msgs))
		return nil
	}
	go func() {
		<-gate
		g.Done(fn(msgs))
	}()
	return nil
}

func (s *fakeSubmitter) insert(msgs []domain.Message) []store.Result {
	return s.store.Insert(context.Background(), msgs)
}

func (s *fakeSubmitter) then(fns ...outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script = append(s.script, fns...)
}

func (s *fakeSubmitter) alwaysDo(fn outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.always = fn
}

func (s *fakeSubmitter) hold() { s.gate = make(chan struct{}) }

func (s *fakeSubmitter) release() { s.gate <- struct{}{} }

func (s *fakeSubmitter) open() {
	if s.gate != nil {
		s.once.Do(func() { close(s.gate) })
	}
}

func (s *fakeSubmitter) sent() [][]domain.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.groups)
}

func (s *fakeSubmitter) landedUnknown(msgs []domain.Message) []store.Result {
	s.insert(msgs)
	return uniform(len(msgs), store.Unknown)
}

func (s *fakeSubmitter) landedDuplicate(msgs []domain.Message) []store.Result {
	s.insert(msgs)
	return uniform(len(msgs), store.Duplicate)
}

func (s *fakeSubmitter) foreignFirst(msgs []domain.Message) []store.Result {
	foreign := make([]domain.Message, len(msgs))
	for i, m := range msgs {
		m.From, m.CID = "mallory", "foreign-"+m.CID
		foreign[i] = m
	}
	s.insert(foreign)
	return s.insert(msgs)
}

func rejected(msgs []domain.Message) []store.Result {
	return uniform(len(msgs), store.Rejected)
}

func lostUnknown(msgs []domain.Message) []store.Result {
	return uniform(len(msgs), store.Unknown)
}

func uniform(n int, o store.Outcome) []store.Result {
	out := make([]store.Result, n)
	for i := range out {
		out[i] = store.Result{Outcome: o, Err: errors.New(o.String())}
	}
	return out
}
