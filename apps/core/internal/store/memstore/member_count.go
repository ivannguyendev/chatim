package memstore

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MemberCounts = (*Rooms)(nil)

func (s *Rooms) AddMemberCount(ctx context.Context, room uint64, delta int) (domain.MemberCount, error) {
	if err := ctx.Err(); err != nil {
		return domain.MemberCount{}, err
	}
	if err := store.ValidateMemberDelta(delta); err != nil {
		return domain.MemberCount{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[room]
	if !ok {
		return domain.MemberCount{}, domain.ErrRoomNotFound
	}
	r.MemberCount += delta
	r.MemberCountVer++
	s.rooms[room] = r
	return countOf(r), nil
}

func (s *Rooms) CountMembers(ctx context.Context, room uint64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for k, m := range s.members {
		if k.room == room && m.Active() {
			n++
		}
	}
	return n, nil
}

func (s *Rooms) SetMemberCount(ctx context.Context, room, base uint64, count int) (domain.MemberCount, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.MemberCount{}, false, err
	}
	if err := store.ValidateMemberCount(count); err != nil {
		return domain.MemberCount{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[room]
	if !ok || r.MemberCountVer != base {
		return domain.MemberCount{}, false, nil
	}
	r.MemberCount = count
	r.MemberCountVer++
	s.rooms[room] = r
	return countOf(r), true, nil
}

func countOf(r domain.Room) domain.MemberCount {
	return domain.MemberCount{Count: r.MemberCount, Ver: r.MemberCountVer}
}
