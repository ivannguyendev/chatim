package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Rooms) ListMembers(ctx context.Context, room uint64, after store.MemberCursor, limit int) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateMemberList(room, after, limit); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Member{}
	for k, m := range s.members {
		if k.room == room && m.Active() && after.Before(m) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b domain.Member) int {
		return store.CompareMemberOrder(store.MemberCursorOf(a), store.MemberCursorOf(b))
	})
	return out[:min(len(out), limit)], nil
}
