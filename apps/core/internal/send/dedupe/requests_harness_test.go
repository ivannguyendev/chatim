package dedupe

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

const requestTTL = 15 * time.Minute

type scriptedRegistry struct {
	mu       sync.Mutex
	verdicts []Verdict
	err      error
	hang     bool
	reserves [][]Key
	commits  [][]Entry
	aborts   [][]Key
}

func (s *scriptedRegistry) answer(verdicts []Verdict, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verdicts, s.err = verdicts, err
}

func (s *scriptedRegistry) Reserve(ctx context.Context, keys []Key) ([]Verdict, error) {
	s.mu.Lock()
	s.reserves = append(s.reserves, slices.Clone(keys))
	hang, verdicts, err := s.hang, slices.Clone(s.verdicts), s.err
	s.mu.Unlock()
	if hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return verdicts, err
}

func (s *scriptedRegistry) Commit(_ context.Context, entries []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits = append(s.commits, slices.Clone(entries))
	return nil
}

func (s *scriptedRegistry) Abort(_ context.Context, keys []Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aborts = append(s.aborts, slices.Clone(keys))
	return nil
}

func (s *scriptedRegistry) calls() (reserves [][]Key, commits [][]Entry, aborts [][]Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reserves), slices.Clone(s.commits), slices.Clone(s.aborts)
}

func newRequests(t *testing.T, reg Registry) *Requests {
	t.Helper()
	r, err := NewRequests(reg, requestTTL)
	if err != nil {
		t.Fatalf("NewRequests: %v", err)
	}
	return r
}

func expectBegin(t *testing.T, r *Requests, k Key, want RequestStatus) {
	t.Helper()
	got, _, err := r.Begin(t.Context(), k)
	if err != nil || got != want {
		t.Fatalf("Begin(%s) = %v, %v, want %v", k, got, err, want)
	}
}

func requestKey(id string) Key { return RequestKey(42, "alice", id) }
