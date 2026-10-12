package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.PinProjector = (*Rooms)(nil)

func (s *Rooms) PinState(ctx context.Context, room uint64) (domain.PinState, error) {
	if err := ctx.Err(); err != nil {
		return domain.PinState{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.rooms[room]; !ok {
		return domain.PinState{}, domain.ErrRoomNotFound
	}
	st := s.pins[room]
	return domain.PinState{Pins: slices.Clone(st.Pins), Version: st.Version}, nil
}

func (s *Rooms) ApplyPins(ctx context.Context, room, base uint64, st domain.PinState) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, st.Version); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rooms[room]; !ok || s.pins[room].Version != base {
		return false, nil
	}
	s.pins[room] = domain.PinState{Pins: slices.Clone(st.Pins), Version: st.Version}
	return true, nil
}
