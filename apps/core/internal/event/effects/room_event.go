package effects

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const RoomEventName = "room_event"

type RoomEventDeps struct {
	Rooms RoomReader
	JS    publish.JetStream
}

type RoomEvent struct {
	eventPublisher
	rooms RoomReader
	delay time.Duration
}

func NewRoomEvent(deps RoomEventDeps, cfg MessageChangedConfig) (*RoomEvent, error) {
	if deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs rooms and a jetstream client", apperr.ErrInvalidArgument, RoomEventName)
	}
	cfg, err := eventConfig(RoomEventName, cfg)
	if err != nil {
		return nil, err
	}
	e := &RoomEvent{rooms: deps.Rooms, delay: cfg.Delay}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return e, nil
}

func (e *RoomEvent) Effect() Effect {
	return Effect{Name: RoomEventName, Delay: e.delay, Run: e.run}
}

func (e *RoomEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, eventkit.Undeliverable)
}

func (e *RoomEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	room, err := e.rooms.Get(ctx, r.Room)
	switch {
	case err != nil:
		return nil, err
	case room.InfoVer > uint64(r.Version):
		return nil, nil
	case room.InfoVer < uint64(r.Version):
		return nil, store.ErrStaleRead
	}
	return pbconv.RoomUpdated(room), nil
}
