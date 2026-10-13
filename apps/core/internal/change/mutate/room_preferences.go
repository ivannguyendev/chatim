package mutate

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
)

type PreferencesCmd struct {
	Tenant, User string
	Room         uint64
	Change       domain.PreferencesChange
}

type PreferencesResult struct {
	Member  domain.Member
	Changed bool
}

func (m *Mutator) SetRoomPreferences(ctx context.Context, c PreferencesCmd) (PreferencesResult, error) {
	if err := domain.ValidatePreferencesChange(c.Change); err != nil {
		return PreferencesResult{}, err
	}
	req, err := m.d.Access.Admit(ctx, access.SetRoomPreferences, c.Tenant, c.User, c.Room)
	if err != nil {
		return PreferencesResult{}, err
	}
	at := m.now()
	prefs, changed, err := m.d.Rooms.SetPreferences(ctx, req.Room.ID, c.User, c.Change, at)
	if err != nil {
		return PreferencesResult{}, err
	}
	member := req.Member
	member.Preferences = prefs
	if changed {
		m.tell(req.Room.ID, pbconv.RoomPreferencesChanged(req.Room, member, at))
	}
	return PreferencesResult{Member: member, Changed: changed}, nil
}
