package effects

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const HiddenEventName = "hidden_event"

var errNoHide = fmt.Errorf("hidden message %w", apperr.ErrNotFound)

type HiddenEventDeps struct {
	Hidden HiddenLookup
	Rooms  RoomReader
	JS     publish.JetStream
}

type HiddenEvent struct {
	eventPublisher
	hidden HiddenLookup
	delay  time.Duration
}

func NewHiddenEvent(deps HiddenEventDeps, cfg MessageChangedConfig) (*HiddenEvent, error) {
	if deps.Hidden == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs hidden messages, rooms and a jetstream client", apperr.ErrInvalidArgument, HiddenEventName)
	}
	cfg, err := eventConfig(HiddenEventName, cfg)
	if err != nil {
		return nil, err
	}
	e := &HiddenEvent{hidden: deps.Hidden, delay: cfg.Delay}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return e, nil
}

func (e *HiddenEvent) Effect() Effect {
	return Effect{Name: HiddenEventName, Delay: e.delay, Run: e.run}
}

func (e *HiddenEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, hideGone)
}

func hideGone(err error) bool { return gone(err) || errors.Is(err, errNoHide) }

func (e *HiddenEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	h, found, err := e.hidden.Get(ctx, r.User, recordKey(r))
	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, errNoHide
	}
	room, err := e.types.identity(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.MessageHidden(room, r.User, r.Thread, r.Seq, h.At), nil
}
