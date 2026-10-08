package effects

import (
	"cmp"
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const MessageChangedName = "msg_changed"

var errProjectionBehind = fmt.Errorf("message projection behind its edit fact: %w", apperr.ErrUnavailable)

type MessageChangedDeps struct {
	Edits    EditReader
	Messages MessageFinder
	Rooms    RoomReader
	JS       publish.JetStream
}

type MessageChangedConfig struct {
	SubjectRoot string
	Delay       time.Duration
	RoomCache   int
}

type MessageChanged struct {
	deps        MessageChangedDeps
	cfg         MessageChangedConfig
	types       *roomTypes
	republished atomic.Uint64
	dropped     atomic.Uint64
}

func NewMessageChanged(deps MessageChangedDeps, cfg MessageChangedConfig) (*MessageChanged, error) {
	if deps.Edits == nil || deps.Messages == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: msg_changed needs edits, messages, rooms and a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg.Delay = cmp.Or(cfg.Delay, DefaultDelay)
	cfg.RoomCache = cmp.Or(cfg.RoomCache, DefaultRoomCache)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 || cfg.RoomCache < 0 {
		return nil, fmt.Errorf("%w: msg_changed config %+v needs a subject root, a delay and a room cache", apperr.ErrInvalidArgument, cfg)
	}
	return &MessageChanged{deps: deps, cfg: cfg, types: newRoomTypes(deps.Rooms, cfg.RoomCache)}, nil
}

func (e *MessageChanged) Effect() Effect {
	return Effect{Name: MessageChangedName, Delay: e.cfg.Delay, Run: e.run}
}

func (e *MessageChanged) Republished() uint64 { return e.republished.Load() }

func (e *MessageChanged) Dropped() uint64 { return e.dropped.Load() }

func (e *MessageChanged) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	var pending []pendingAck
	for i, r := range recs {
		if r.Version == 0 {
			continue
		}
		ev, err := e.event(ctx, r)
		switch {
		case gone(err):
			e.dropped.Add(1)
			continue
		case err != nil:
			errs[i] = err
			continue
		}
		msg, err := publish.Message(e.cfg.SubjectRoot, r.Room, ev)
		if err != nil {
			e.dropped.Add(1)
			continue
		}
		pending = send(e.deps.JS, msg, i, errs, pending)
	}
	awaitAcks(ctx, pending, errs, countStored(&e.republished))
	return errs
}

func (e *MessageChanged) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	key := recordKey(r)
	fact, err := e.deps.Edits.At(ctx, key, r.Version)
	if err != nil {
		return nil, err
	}
	typ, err := e.types.get(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	found, err := e.deps.Messages.Find(ctx, r.Room, []store.MsgKey{key})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMessageNotFound
	case found[0].Version < fact.Version:
		return nil, errProjectionBehind
	}
	return pbconv.MessageChanged(typ, found[0], fact), nil
}
