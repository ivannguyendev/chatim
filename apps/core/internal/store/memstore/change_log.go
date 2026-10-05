package memstore

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type logged struct {
	kind store.ChangeKind
	msg  domain.Message
	room domain.Room
	at   time.Time
}

func (s *Messages) appendLog(l logged) {
	l.at = time.Now()
	s.log = append(s.log, l)
	close(s.grew)
	s.grew = make(chan struct{})
}

func (s *Messages) appendRoom(r domain.Room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLog(logged{kind: store.RoomInserted, room: r})
}

func (s *Messages) logAt(i int) (logged, <-chan struct{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i < len(s.log) {
		return s.log[i], nil, true
	}
	return logged{}, s.grew, false
}

func (s *Messages) logLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.log)
}

func (s *Rooms) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}
