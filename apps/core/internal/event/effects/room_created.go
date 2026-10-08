package effects

import (
	"cmp"
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const RoomCreatedName = "room_created"

type RoomCreatedDeps struct {
	Rooms RoomReader
	JS    publish.JetStream
}

type RoomCreatedConfig struct {
	SubjectRoot string
	Delay       time.Duration
}

type RoomCreated struct {
	deps        RoomCreatedDeps
	cfg         RoomCreatedConfig
	republished atomic.Uint64
	dropped     atomic.Uint64
}

func NewRoomCreated(deps RoomCreatedDeps, cfg RoomCreatedConfig) (*RoomCreated, error) {
	if deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: room_created needs rooms and a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg.Delay = cmp.Or(cfg.Delay, DefaultDelay)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 {
		return nil, fmt.Errorf("%w: room_created config %+v needs a subject root and a delay", apperr.ErrInvalidArgument, cfg)
	}
	return &RoomCreated{deps: deps, cfg: cfg}, nil
}

func (e *RoomCreated) Effect() Effect {
	return Effect{Name: RoomCreatedName, Delay: e.cfg.Delay, Run: e.run}
}

func (e *RoomCreated) Republished() uint64 { return e.republished.Load() }

func (e *RoomCreated) Dropped() uint64 { return e.dropped.Load() }

func (e *RoomCreated) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	var pending []pendingAck
	for i, r := range recs {
		room, err := e.deps.Rooms.Get(ctx, r.Room)
		switch {
		case undeliverable(err):
			e.dropped.Add(1)
			continue
		case err != nil:
			errs[i] = err
			continue
		}
		msg, err := publish.Message(e.cfg.SubjectRoot, room.ID, pbconv.RoomCreated(room))
		if err != nil {
			e.dropped.Add(1)
			continue
		}
		pending = send(e.deps.JS, msg, i, errs, pending)
	}
	awaitAcks(ctx, pending, errs, countAll(&e.republished))
	return errs
}
