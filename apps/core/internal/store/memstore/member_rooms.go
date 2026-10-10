package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func (s *Rooms) RoomsOf(ctx context.Context, tenant, user string) ([]uint64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := domain.ValidUser(user); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []uint64{}
	for k, m := range s.members {
		if k.user == user && m.Tenant == tenant && m.Active() {
			out = append(out, k.room)
		}
	}
	slices.Sort(out)
	return out, nil
}
