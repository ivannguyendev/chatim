package memstore

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	mu        sync.RWMutex
	rooms     map[uint64]domain.Room
	members   map[memberKey]domain.Member
	pins      map[uint64]domain.PinState
	ownersVer map[uint64]uint64
	log       *Messages
}

func NewRooms() *Rooms {
	return &Rooms{
		rooms: make(map[uint64]domain.Room), members: make(map[memberKey]domain.Member),
		pins: make(map[uint64]domain.PinState), ownersVer: make(map[uint64]uint64),
	}
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
	r.MemberCountVer = 1
	s.rooms[r.ID] = r
	if s.log != nil {
		s.log.appendFact(logged{kind: store.RoomInserted, room: r})
	}
	for _, m := range members {
		k := memberKey{m.Room, m.User}
		if _, ok := s.members[k]; !ok {
			s.members[k] = stamped(store.CreationMember(r, m))
			s.logMemberLocked(store.MemberChanged, s.members[k])
		}
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
	if !ok || !m.Active() {
		return domain.Member{}, domain.ErrNotMember
	}
	return m, nil
}

func (s *Rooms) ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, bool, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, false, err
	}
	if err := store.ValidateMarkTime(at); err != nil {
		return time.Time{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{room, user}
	m, ok := s.members[k]
	if !ok || !m.Active() {
		return time.Time{}, false, domain.ErrNotMember
	}
	if at = toMillis(at); !at.After(m.ClearedAt) {
		return m.ClearedAt, false, nil
	}
	m.ClearedAt, m.LastChangeAt = at, later(m.LastChangeAt, at)
	s.members[k] = m
	s.logMemberLocked(store.HistoryCleared, m)
	return at, true, nil
}
