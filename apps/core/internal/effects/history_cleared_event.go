package effects

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const HistoryClearedEventName = "history_cleared_event"

type HistoryClearedEvent struct{ memberEffect }

func NewHistoryClearedEvent(deps MemberEventDeps, cfg MessageChangedConfig) (*HistoryClearedEvent, error) {
	e := &HistoryClearedEvent{}
	if err := e.setup(HistoryClearedEventName, deps, cfg); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *HistoryClearedEvent) Effect() Effect {
	return Effect{Name: HistoryClearedEventName, Delay: e.delay, Run: e.run}
}

func (e *HistoryClearedEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, memberGone)
}

func (e *HistoryClearedEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	doc, err := e.current(ctx, r.Room, r.User)
	switch {
	case err != nil:
		return nil, err
	case doc.ClearedAt.IsZero():
		return nil, errNotCleared
	}
	room, err := e.types.identity(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.HistoryCleared(room, r.User, doc.ClearedAt, e.stamp()), nil
}
