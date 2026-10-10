package mutate_test

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type countCall struct {
	key    store.MsgKey
	deltas []store.EmojiDelta
}

type spyCounts struct {
	inner mutate.ReactionCounts
	log   *callLog
	mu    sync.Mutex
	err   error
	calls []countCall
}

func (s *spyCounts) AddReactionCounts(ctx context.Context, key store.MsgKey, deltas []store.EmojiDelta) (domain.ReactionSummary, error) {
	s.log.add("count")
	s.mu.Lock()
	s.calls = append(s.calls, countCall{key: key, deltas: slices.Clone(deltas)})
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return domain.ReactionSummary{}, err
	}
	return s.inner.AddReactionCounts(ctx, key, deltas)
}

func (s *spyCounts) list() []countCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

type fakeMessageTimers struct {
	log      *callLog
	mu       sync.Mutex
	seq      uint64
	armed    map[uint64]store.MsgKey
	counters []string
	err      error
}

func (f *fakeMessageTimers) ArmMessageCountCheck(_ context.Context, key store.MsgKey, counter string) (work.Timer, error) {
	f.log.add("arm")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return work.Timer{}, f.err
	}
	f.seq++
	f.armed[f.seq] = key
	f.counters = append(f.counters, counter)
	return work.Timer{Seq: f.seq}, nil
}

func (f *fakeMessageTimers) Disarm(_ context.Context, t work.Timer) {
	f.log.add("disarm")
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.armed, t.Seq)
}

func (f *fakeMessageTimers) pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.armed)
}

type loggedWrites struct {
	store.Interactions
	log *callLog
	err error
}

func (w loggedWrites) SetReaction(ctx context.Context, r domain.Reaction) (domain.Reaction, bool, error) {
	w.log.add("write")
	if w.err != nil {
		return domain.Reaction{}, false, w.err
	}
	return w.Interactions.SetReaction(ctx, r)
}

func (w loggedWrites) RemoveReaction(ctx context.Context, key store.MsgKey, user string, at time.Time) (domain.Reaction, bool, error) {
	w.log.add("write")
	if w.err != nil {
		return domain.Reaction{}, false, w.err
	}
	return w.Interactions.RemoveReaction(ctx, key, user, at)
}

type reactionParts struct {
	reactCalls *callLog
	counts     *spyCounts
	msgTimers  *fakeMessageTimers
	writes     loggedWrites
}

func newReactionParts(counts mutate.ReactionCounts, interactions store.Interactions) reactionParts {
	calls := &callLog{}
	return reactionParts{
		reactCalls: calls,
		counts:     &spyCounts{inner: counts, log: calls},
		msgTimers:  &fakeMessageTimers{log: calls, armed: map[uint64]store.MsgKey{}},
		writes:     loggedWrites{Interactions: interactions, log: calls},
	}
}
