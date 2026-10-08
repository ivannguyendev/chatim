package effects

import (
	"cmp"
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type eventPublisher struct {
	js          publish.JetStream
	root        string
	types       *roomTypes
	republished atomic.Uint64
	dropped     atomic.Uint64
}

func eventConfig(name string, cfg MessageChangedConfig) (MessageChangedConfig, error) {
	cfg.Delay = cmp.Or(cfg.Delay, DefaultDelay)
	cfg.RoomCache = cmp.Or(cfg.RoomCache, DefaultRoomCache)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 || cfg.RoomCache < 0 {
		return MessageChangedConfig{}, fmt.Errorf("%w: %s config %+v needs a subject root, a delay and a room cache", apperr.ErrInvalidArgument, name, cfg)
	}
	return cfg, nil
}

func (p *eventPublisher) init(js publish.JetStream, root string, rooms RoomReader, cache int) {
	p.js, p.root, p.types = js, root, newRoomTypes(rooms, cache)
}

func (p *eventPublisher) Republished() uint64 { return p.republished.Load() }

func (p *eventPublisher) Dropped() uint64 { return p.dropped.Load() }

func (p *eventPublisher) each(ctx context.Context, recs []work.Record, build func(context.Context, work.Record) (*chatimv1.Event, error), drop func(error) bool) []error {
	errs := make([]error, len(recs))
	var pending []pendingAck
	for i, r := range recs {
		ev, err := build(ctx, r)
		switch {
		case drop(err):
			p.dropped.Add(1)
		case err != nil:
			errs[i] = err
		case ev != nil:
			pending = p.queue(pending, errs, i, r.Room, ev)
		}
	}
	awaitAcks(ctx, pending, errs, countStored(&p.republished))
	return errs
}

func (p *eventPublisher) queue(pending []pendingAck, errs []error, i int, room uint64, ev *chatimv1.Event) []pendingAck {
	msg, err := publish.Message(p.root, room, ev)
	if err != nil {
		p.dropped.Add(1)
		return pending
	}
	return send(p.js, msg, i, errs, pending)
}
