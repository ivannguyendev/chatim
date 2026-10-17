package memstore

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.RoomPreferences = (*Rooms)(nil)

func (s *Rooms) SetPreferences(ctx context.Context, room uint64, user string, c domain.PreferencesChange, at time.Time) (domain.RoomPreferences, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.RoomPreferences{}, false, err
	}
	if err := store.ValidatePreferences(room, user, c, at); err != nil {
		return domain.RoomPreferences{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{room, user}
	m, ok := s.members[k]
	if !ok || !m.Active() {
		return domain.RoomPreferences{}, false, domain.ErrNotMember
	}
	next, changed := c.Apply(m.Preferences, at)
	if !changed {
		return m.Preferences, false, nil
	}
	m.Preferences, m.LastChangeAt = next, later(m.LastChangeAt, toMillis(at))
	s.members[k] = m
	s.memberFlags.raise(k, store.BitPreferences, time.Now())
	return next, true, nil
}
