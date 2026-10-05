package memstore

import (
	"context"
	"fmt"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var (
	_ store.Rooms          = (*Rooms)(nil)
	_ store.HistoryClearer = (*Rooms)(nil)
)

type memberKey struct {
	room uint64
	user string
}

type Rooms struct {
	mu      sync.RWMutex
	rooms   map[uint64]domain.Room
	members map[memberKey]domain.Member
	log     *Messages
}

func NewRooms() *Rooms {
	return &Rooms{rooms: make(map[uint64]domain.Room), members: make(map[memberKey]domain.Member)}
}

func (s *Rooms) Create(ctx context.Context, r domain.Room, members []domain.Member) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateRoom(r, members); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rooms[r.ID]; ok {
		return fmt.Errorf("create room %d: %w", r.ID, store.ErrRoomExists)
	}
	s.rooms[r.ID] = r
	for _, m := range members {
		k := memberKey{m.Room, m.User}
		if _, ok := s.members[k]; !ok {
			s.members[k] = m
		}
	}
	if s.log != nil {
		s.log.appendRoom(r)
	}
	return nil
}

func (s *Rooms) Get(ctx context.Context, id uint64) (domain.Room, error) {
	if err := ctx.Err(); err != nil {
		return domain.Room{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rooms[id]
	if !ok {
		return domain.Room{}, domain.ErrRoomNotFound
	}
	return r, nil
}

func (s *Rooms) Member(ctx context.Context, room uint64, user string) (domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return domain.Member{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.members[memberKey{room, user}]
	if !ok {
		return domain.Member{}, domain.ErrNotMember
	}
	return m, nil
}

func (s *Rooms) ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{room, user}
	m, ok := s.members[k]
	if !ok {
		return 0, domain.ErrNotMember
	}
	m.ClearedBeforeSeq = max(m.ClearedBeforeSeq, seq)
	s.members[k] = m
	return m.ClearedBeforeSeq, nil
}
