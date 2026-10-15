package memstore

import (
	"encoding/binary"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Rooms) pendingDocs(c store.StateCollection) []pendingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c == store.StateMembers {
		return flagged(s.memberFlags, func(k memberKey) (store.PendingDoc, []byte) {
			return store.PendingDoc{Collection: store.StateMembers, Room: k.room, User: k.user}, keys.Member(k.room, k.user)
		})
	}
	return flagged(s.roomFlags, func(room uint64) (store.PendingDoc, []byte) {
		return store.PendingDoc{Collection: store.StateRooms, Room: room}, binary.BigEndian.AppendUint64(nil, room)
	})
}

func (s *Rooms) applyStates(targets []store.StateTarget, mark bool, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range targets {
		if t.Collection == store.StateMembers {
			k := memberKey{t.Room, t.User}
			if m, ok := s.members[k]; ok && memberVersionIs(m, t) {
				s.memberFlags.apply(k, t.Bit, mark, at)
			}
			continue
		}
		if r, ok := s.rooms[t.Room]; ok && roomVersionIs(r, t) {
			s.roomFlags.apply(t.Room, t.Bit, mark, at)
		}
	}
}

func (s *Rooms) seedState(t store.StateTarget, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Collection == store.StateMembers {
		s.seedMemberLocked(t, at)
		return nil
	}
	r, ok := s.rooms[t.Room]
	if !ok {
		r = domain.Room{ID: t.Room}
	}
	switch t.Bit {
	case store.BitMemberCount:
		r.MemberCountVer = t.Ver
	case store.BitRoomInfo:
		r.InfoVer = t.Ver
	default:
	}
	s.rooms[t.Room] = r
	s.roomFlags.raise(t.Room, t.Bit, at)
	return nil
}

func (s *Rooms) seedMemberLocked(t store.StateTarget, at time.Time) {
	k := memberKey{t.Room, t.User}
	m, ok := s.members[k]
	if !ok {
		m = domain.Member{Room: t.Room, User: t.User}
	}
	switch t.Bit {
	case store.BitMember:
		m.Ver = uint32(min(t.Ver, math.MaxUint32))
	case store.BitReadPosition:
		m.ReadVer = t.Ver
	case store.BitHistoryCleared:
		m.ClearedAt = millis(t.At)
	case store.BitPreferences:
		m.Preferences.Ver = t.Ver
	}
	s.members[k] = m
	s.memberFlags.raise(k, t.Bit, at)
}

func memberVersionIs(m domain.Member, t store.StateTarget) bool {
	switch t.Bit {
	case store.BitMember:
		return uint64(m.Ver) == t.Ver
	case store.BitReadPosition:
		return m.ReadVer == t.Ver
	case store.BitHistoryCleared:
		return sameMillis(m.ClearedAt, t.At)
	case store.BitPreferences:
		return m.Preferences.Ver == t.Ver
	default:
		return false
	}
}

func roomVersionIs(r domain.Room, t store.StateTarget) bool {
	switch t.Bit {
	case store.BitMemberCount:
		return r.MemberCountVer == t.Ver
	case store.BitRoomInfo:
		return r.InfoVer == t.Ver
	default:
		return true
	}
}
