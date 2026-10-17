package memstore

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MemberIDs = (*Rooms)(nil)

func (s *Rooms) MemberUserIDs(ctx context.Context, room uint64) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateMemberIDsRoom(room); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []string{}
	for k, m := range s.members {
		if k.room == room && m.Active() {
			out = append(out, k.user)
		}
	}
	return out, nil
}
