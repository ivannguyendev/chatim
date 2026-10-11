package memstore

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.SharedMentions = (*Rooms)(nil)

func (s *Rooms) RaiseSharedMention(ctx context.Context, room uint64, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateSharedMention(room, at); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[room]
	if !ok {
		return domain.ErrRoomNotFound
	}
	r.LastSharedMentionAt = laterOf(r.LastSharedMentionAt, at.UTC())
	s.rooms[room] = r
	return nil
}

func (s *Rooms) SharedMentionTimes(ctx context.Context, rooms []uint64) (map[uint64]time.Time, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateSharedMentionRooms(rooms); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[uint64]time.Time)
	for _, id := range rooms {
		if r, ok := s.rooms[id]; ok && !r.LastSharedMentionAt.IsZero() {
			out[id] = r.LastSharedMentionAt
		}
	}
	return out, nil
}
