package memstore

import (
	"cmp"
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Rooms) RoomsOf(ctx context.Context, tenant, user string) ([]store.MemberRoom, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := domain.ValidUser(user); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []store.MemberRoom{}
	for k, m := range s.members {
		if k.user == user && m.Tenant == tenant && m.Active() {
			out = append(out, store.MemberRoom{Room: k.room, JoinedAt: m.JoinedAt, ClearedAt: m.ClearedAt, ReadSeq: m.ReadSeq})
		}
	}
	slices.SortFunc(out, func(a, b store.MemberRoom) int { return cmp.Compare(a.Room, b.Room) })
	return out, nil
}
