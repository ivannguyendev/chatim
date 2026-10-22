package memstore

import (
	"cmp"
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Rooms) RoomVersionsOf(ctx context.Context, tenant, user string) ([]store.RoomVersion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := domain.ValidUser(user); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []store.RoomVersion{}
	for k, m := range s.members {
		if k.user != user || m.Tenant != tenant {
			continue
		}
		switch m.State {
		case domain.MemberActive, domain.MemberRemoved:
			out = append(out, store.RoomVersion{Room: k.room, Ver: m.Ver, Left: m.State == domain.MemberRemoved})
		}
	}
	slices.SortFunc(out, func(a, b store.RoomVersion) int { return cmp.Compare(a.Room, b.Room) })
	return out, nil
}
