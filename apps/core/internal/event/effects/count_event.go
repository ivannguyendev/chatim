package effects

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const CountEventName = "count_event"

type CountEventDeps struct {
	Messages MessageFinder
	Rooms    RoomReader
	JS       publish.JetStream
	Now      func() time.Time
}

type CountEvent struct {
	eventPublisher
	messages MessageFinder
	now      func() time.Time
	delay    time.Duration
}

func NewCountEvent(deps CountEventDeps, cfg MessageChangedConfig) (*CountEvent, error) {
	if deps.Messages == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs messages, rooms and a jetstream client", apperr.ErrInvalidArgument, CountEventName)
	}
	cfg, err := eventConfig(CountEventName, cfg)
	if err != nil {
		return nil, err
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	e := &CountEvent{messages: deps.Messages, now: deps.Now, delay: cfg.Delay}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return e, nil
}

func (e *CountEvent) Effect() Effect {
	return Effect{Name: CountEventName, Delay: e.delay, Run: e.run}
}

func (e *CountEvent) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	groups := groupRecords(recs, recordKey)
	var pending []pendingAck
	for _, g := range groups {
		ev, err := e.event(ctx, g.key)
		switch {
		case gone(err):
			g.drop(&e.dropped)
		case err != nil:
			g.fail(errs, err)
		case ev != nil:
			pending = e.queue(pending, errs, g.indexes[0], g.key.Room, ev)
		}
	}
	awaitAcks(ctx, pending, errs, countStored(&e.republished))
	for _, g := range groups {
		g.share(errs)
	}
	return errs
}

func (e *CountEvent) event(ctx context.Context, key store.MsgKey) (*chatimv1.Event, error) {
	found, err := e.messages.Find(ctx, key.Room, []store.MsgKey{key})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMessageNotFound
	case found[0].Reactions.Version == 0:
		return nil, nil
	}
	typ, err := e.types.get(ctx, key.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.CountsChanged(typ, found[0], e.now().UTC().Truncate(time.Millisecond)), nil
}
