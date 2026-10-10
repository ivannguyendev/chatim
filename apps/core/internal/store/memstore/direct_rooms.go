package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.DirectRooms = (*DirectRooms)(nil)

type DirectRooms struct {
	mu    sync.Mutex
	rooms map[string]uint64
}

func NewDirectRooms() *DirectRooms {
	return &DirectRooms{rooms: make(map[string]uint64)}
}

func (s *DirectRooms) Claim(ctx context.Context, tenant, a, b string, candidate uint64, at time.Time) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := store.ValidateDirectClaim(tenant, a, b, candidate, at); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := domain.DirectKey(tenant, a, b)
	if room, ok := s.rooms[key]; ok {
		return room, nil
	}
	s.rooms[key] = candidate
	return candidate, nil
}

func (s *DirectRooms) Repoint(ctx context.Context, tenant, a, b string, old, next uint64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateDirectRepoint(tenant, a, b, next); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := domain.DirectKey(tenant, a, b)
	if room, ok := s.rooms[key]; !ok || room != old {
		return false, nil
	}
	s.rooms[key] = next
	return true, nil
}
