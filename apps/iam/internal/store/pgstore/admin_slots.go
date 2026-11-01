package pgstore

import (
	"context"
	"sync/atomic"
)

const defaultAdminDBSlots = 4

type adminSlots struct {
	free     chan struct{}
	timeouts atomic.Uint64
}

func newAdminSlots(n int) *adminSlots {
	if n <= 0 {
		n = defaultAdminDBSlots
	}
	return &adminSlots{free: make(chan struct{}, n)}
}

func (s *adminSlots) enter(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.free <- struct{}{}:
		return func() { <-s.free }, nil
	case <-ctx.Done():
		s.timeouts.Add(1)
		return nil, ctx.Err()
	}
}

func (s *Store) AdminSlotTimeouts() uint64 { return s.adminSlots.timeouts.Load() }
