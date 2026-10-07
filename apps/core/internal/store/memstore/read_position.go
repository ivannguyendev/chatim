package memstore

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.ReadPositions = (*Rooms)(nil)

func (s *Rooms) MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPosition, bool, error) {
	return s.moveRead(ctx, room, user, seq, func(cur uint64) bool { return cur < seq })
}

func (s *Rooms) MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPosition, bool, error) {
	return s.moveRead(ctx, room, user, to, func(cur uint64) bool { return cur > to })
}

func (s *Rooms) moveRead(ctx context.Context, room uint64, user string, seq uint64, moves func(cur uint64) bool) (domain.ReadPosition, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReadPosition{}, false, err
	}
	if err := store.ValidateReadSeq(seq); err != nil {
		return domain.ReadPosition{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{room, user}
	m, ok := s.members[k]
	if !ok || !m.Active() {
		return domain.ReadPosition{}, false, domain.ErrNotMember
	}
	if !moves(m.ReadSeq) {
		return positionOf(m), false, nil
	}
	m.ReadSeq, m.ReadVer, m.LastChangeAt = seq, m.ReadVer+1, toMillis(time.Now())
	s.members[k] = m
	return positionOf(m), true, nil
}

func positionOf(m domain.Member) domain.ReadPosition {
	return domain.ReadPosition{Seq: m.ReadSeq, Ver: m.ReadVer}
}
