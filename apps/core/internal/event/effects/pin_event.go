package effects

import (
	"context"
	"errors"
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

const PinEventName = "pin_event"

type PinEventDeps struct {
	Pins     PinFacts
	Messages MessageFinder
	Rooms    RoomReader
	JS       publish.JetStream
}

type PinEvent struct {
	eventPublisher
	deps  PinEventDeps
	delay time.Duration
}

func NewPinEvent(deps PinEventDeps, cfg MessageChangedConfig) (*PinEvent, error) {
	if deps.Pins == nil || deps.Messages == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs pins, messages, rooms and a jetstream client", apperr.ErrInvalidArgument, PinEventName)
	}
	cfg, err := eventConfig(PinEventName, cfg)
	if err != nil {
		return nil, err
	}
	e := &PinEvent{deps: deps, delay: cfg.Delay}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return e, nil
}

func (e *PinEvent) Effect() Effect {
	return Effect{Name: PinEventName, Delay: e.delay, Run: e.run}
}

func (e *PinEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, pinGone)
}

func pinGone(err error) bool { return gone(err) || errors.Is(err, store.ErrPinNotFound) }

func (e *PinEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	fact, err := e.deps.Pins.At(ctx, r.Room, r.Seq)
	if err != nil {
		return nil, err
	}
	key := store.MsgKey{Room: fact.Room, Thread: fact.Thread, Seq: fact.Seq}
	found, err := e.deps.Messages.Find(ctx, fact.Room, []store.MsgKey{key})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMessageNotFound
	}
	typ, err := e.types.get(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.PinChanged(typ, found[0], fact), nil
}
