package memstore

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type logged struct {
	kind     store.ChangeKind
	msg      domain.Message
	room     domain.Room
	edit     domain.Edit
	reaction domain.Reaction
	pin      domain.PinAction
	member   domain.Member
	hidden   domain.HiddenMessage
	at       time.Time
}

type FeedOption func(log *Messages)

func WithReactions(r *Reactions) FeedOption { return func(log *Messages) { r.attach(log) } }

func WithPins(p *Pins) FeedOption { return func(log *Messages) { p.attach(log) } }

func WithHidden(h *Hidden) FeedOption { return func(log *Messages) { h.attach(log) } }

func (s *Messages) appendLog(l logged) {
	l.at = time.Now()
	s.log = append(s.log, l)
	close(s.grew)
	s.grew = make(chan struct{})
}

func (s *Messages) appendFact(l logged) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLog(l)
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

func (s *Edits) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}

func (s *Reactions) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}

func (s *Pins) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}

func (s *Hidden) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}

func (s *Rooms) logMemberLocked(kind store.ChangeKind, m domain.Member) {
	if s.log == nil {
		return
	}
	if kind == store.ReadChanged {
		m = domain.Member{Room: m.Room, User: m.User, ReadVer: m.ReadVer}
	}
	if kind == store.HistoryCleared {
		m = domain.Member{Room: m.Room, User: m.User}
	}
	s.log.appendFact(logged{kind: kind, member: m})
}
