package effects

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const ReadEventName = "read_event"

type ReadEvent struct{ memberEffect }

func NewReadEvent(deps MemberEventDeps, cfg MessageChangedConfig) (*ReadEvent, error) {
	e := &ReadEvent{}
	if err := e.setup(ReadEventName, deps, cfg); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *ReadEvent) Effect() Effect {
	return Effect{Name: ReadEventName, Delay: e.delay, Run: e.run}
}

func (e *ReadEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, memberGone)
}

func (e *ReadEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	doc, err := e.current(ctx, r.Room, r.User)
	switch {
	case err != nil:
		return nil, err
	case doc.ReadVer > uint64(r.Version):
		return nil, nil
	case doc.ReadVer < uint64(r.Version):
		return nil, store.ErrStaleRead
	}
	room, err := e.types.identity(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.ReadUpdated(room, r.User, domain.ReadPosition{Seq: doc.ReadSeq, Ver: doc.ReadVer}, e.stamp()), nil
}
