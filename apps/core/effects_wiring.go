package main

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type effectSet struct {
	workers         *effects.Workers
	msgCreated      *effects.MessageCreated
	roomCreated     *effects.RoomCreated
	editProjection  *effects.EditProjection
	msgChanged      *effects.MessageChanged
	reactionCounter *effects.ReactionCounter
	reactionEvent   *effects.ReactionEvent
	pinProjection   *effects.PinProjection
	pinEvent        *effects.PinEvent
}

func wireEffects(cfg config.Config, cl *clients, st *mongostore.Store, marks *eventmark.Store, owner effects.Owner, log *slog.Logger) (effectSet, error) {
	fx := effectSet{}
	var err error
	fx.msgCreated, err = effects.NewMessageCreated(
		effects.MessageCreatedDeps{Marks: marks, Messages: st, Rooms: st, JS: cl.effectsJS},
		effects.MessageCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire msg_created effect: %w", err)
	}
	fx.roomCreated, err = effects.NewRoomCreated(
		effects.RoomCreatedDeps{Rooms: st, JS: cl.effectsJS},
		effects.RoomCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire room_created effect: %w", err)
	}
	fx.editProjection, err = effects.NewEditProjection(effects.EditProjectionDeps{Edits: st, Messages: st, Purger: st})
	if err != nil {
		return effectSet{}, fmt.Errorf("wire edit_projection effect: %w", err)
	}
	fx.msgChanged, err = effects.NewMessageChanged(
		effects.MessageChangedDeps{Edits: st, Messages: st, Rooms: st, JS: cl.effectsJS},
		effects.MessageChangedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire msg_changed effect: %w", err)
	}
	if err = fx.wireReactionPinEffects(cfg, cl, st); err != nil {
		return effectSet{}, err
	}
	activity := effects.NewRoomActivity(st)
	registry := effects.Registry{
		store.MessageInserted: {activity.Effect(), fx.msgCreated.Effect()},
		store.RoomInserted:    {fx.roomCreated.Effect()},
		store.EditInserted:    {activity.Effect(), fx.editProjection.Effect(), fx.msgChanged.Effect()},
		store.ReactionChanged: {activity.Effect(), fx.reactionCounter.Effect(), fx.reactionEvent.Effect()},
		store.PinInserted:     {activity.Effect(), fx.pinProjection.Effect(), fx.pinEvent.Effect()},
	}
	fx.workers, err = effects.New(effects.Deps{
		Queue:    func(p int) work.Queue { return work.NewQueue(cl.effectsJS, cfg.Work.Name, p, cfg.Effects.RetryDelay) },
		Owner:    owner,
		Registry: registry,
	}, cfg.Effects, log)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire effect workers: %w", err)
	}
	return fx, nil
}

func (fx effectSet) counters() map[string]effectCounters {
	return map[string]effectCounters{
		fx.msgCreated.Effect().Name:      {republished: fx.msgCreated.Republished, dropped: fx.msgCreated.Dropped},
		fx.roomCreated.Effect().Name:     {republished: fx.roomCreated.Republished, dropped: fx.roomCreated.Dropped},
		fx.msgChanged.Effect().Name:      {republished: fx.msgChanged.Republished, dropped: fx.msgChanged.Dropped},
		fx.editProjection.Effect().Name:  {dropped: fx.editProjection.Dropped},
		fx.reactionCounter.Effect().Name: {republished: fx.reactionCounter.Republished, dropped: fx.reactionCounter.Dropped},
		fx.reactionEvent.Effect().Name:   {republished: fx.reactionEvent.Republished, dropped: fx.reactionEvent.Dropped},
		fx.pinEvent.Effect().Name:        {republished: fx.pinEvent.Republished, dropped: fx.pinEvent.Dropped},
		fx.pinProjection.Effect().Name:   {dropped: fx.pinProjection.Dropped},
	}
}

func (fx effectSet) counterRepairs() map[string]func() uint64 {
	return map[string]func() uint64{pbconv.ReactionsCounter: fx.reactionCounter.Repaired}
}
