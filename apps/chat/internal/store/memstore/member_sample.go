package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
)

func (s *Rooms) MemberSample(ctx context.Context, room uint64, limit int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateMemberSample(room, limit); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var users []string
	for k, m := range s.members {
		if k.room == room && m.Active() {
			users = append(users, k.user)
		}
	}
	slices.Sort(users)
	return users[:min(len(users), limit)], nil
}

func (s *Rooms) SetAvatarUsers(ctx context.Context, room, ver uint64, next []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateAvatarUsers(room, ver, next); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[room]
	if !ok || r.AvatarVer != ver {
		return domain.ErrRetryLater
	}
	r.AvatarUsers, r.AvatarVer = append([]string{}, next...), ver+1
	s.rooms[room] = r
	return nil
}
