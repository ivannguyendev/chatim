package main

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type effectSet struct {
	workers     *effects.Workers
	msgCreated  *effects.MessageCreated
	roomCreated *effects.RoomCreated
}

func wireEffects(cfg config.Config, cl *clients, st *mongostore.Store, marks *eventmark.Store, owner effects.Owner, log *slog.Logger) (effectSet, error) {
	msgCreated, err := effects.NewMessageCreated(
		effects.MessageCreatedDeps{Marks: marks, Messages: st, Rooms: st, JS: cl.effectsJS},
		effects.MessageCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire msg_created effect: %w", err)
	}
	roomCreated, err := effects.NewRoomCreated(
		effects.RoomCreatedDeps{Rooms: st, JS: cl.effectsJS},
		effects.RoomCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire room_created effect: %w", err)
	}
	activity := effects.NewRoomActivity(st)
	registry := effects.Registry{
		store.MessageInserted: {activity.Effect(), msgCreated.Effect()},
		store.RoomInserted:    {roomCreated.Effect()},
	}
	workers, err := effects.New(effects.Deps{
		Queue:    func(p int) work.Queue { return work.NewQueue(cl.effectsJS, cfg.Work.Name, p) },
		Owner:    owner,
		Registry: registry,
	}, cfg.Effects, log)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire effect workers: %w", err)
	}
	return effectSet{workers: workers, msgCreated: msgCreated, roomCreated: roomCreated}, nil
}
