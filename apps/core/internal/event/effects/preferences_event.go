package effects

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const PreferencesEventName = "preferences_event"

type PreferencesEvent struct{ memberEffect }

func NewPreferencesEvent(deps MemberEventDeps, cfg MessageChangedConfig) (*PreferencesEvent, error) {
	e := &PreferencesEvent{}
	if err := e.setup(PreferencesEventName, deps, cfg); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *PreferencesEvent) Effect() Effect {
	return Effect{Name: PreferencesEventName, Delay: e.delay, Run: e.run}
}

func (e *PreferencesEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, memberGone)
}

func (e *PreferencesEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	doc, err := e.current(ctx, r.Room, r.User)
	switch {
	case err != nil:
		return nil, err
	case doc.Preferences.Ver > uint64(r.Version):
		return nil, nil
	case doc.Preferences.Ver < uint64(r.Version):
		return nil, store.ErrStaleRead
	}
	room, err := e.types.identity(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.RoomPreferencesChanged(room, doc, e.stamp()), nil
}
