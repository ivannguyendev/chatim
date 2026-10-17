package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.SweepCheckpoints = (*SweepCheckpoints)(nil)

type SweepCheckpoints struct {
	mu    sync.Mutex
	marks map[store.StateCollection]time.Time
}

func NewSweepCheckpoints() *SweepCheckpoints {
	return &SweepCheckpoints{marks: make(map[store.StateCollection]time.Time)}
}

func (s *SweepCheckpoints) Get(ctx context.Context, c store.StateCollection) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	if err := store.ValidateStateCollection(c); err != nil {
		return time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.marks[c], nil
}

func (s *SweepCheckpoints) Advance(ctx context.Context, c store.StateCollection, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateCheckpoint(c, at); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at = millis(at); at.After(s.marks[c]) {
		s.marks[c] = at
	}
	return nil
}
