package main

import (
	"fmt"
	"log/slog"
	"maps"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
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

	memberEvent       *effects.MemberEvent
	memberCountEvent  *effects.MemberCountEvent
	readEvent         *effects.ReadEvent
	hiddenEvent       *effects.HiddenEvent
	historyCleared    *effects.HistoryClearedEvent
	memberCountRepair *effects.MemberCountRepair
}

func wireEffects(cfg config.Config, cl *clients, st *mongostore.Store, marks *eventmark.Store, owner effects.Owner, timers effects.CountTimers, log *slog.Logger) (effectSet, error) {
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
	if err = fx.wireMemberEffects(cfg, cl, st, timers); err != nil {
		return effectSet{}, err
	}
	fx.workers, err = effects.New(effects.Deps{
		Queue:    func(p int) work.Queue { return work.NewQueue(cl.effectsJS, cfg.Work.Name, p, cfg.Effects.RetryDelay) },
		Owner:    owner,
		Registry: fx.registry(effects.NewRoomActivity(st).Effect()),
	}, cfg.Effects, log)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire effect workers: %w", err)
	}
	return fx, nil
}

func (fx effectSet) registry(activity effects.Effect) effects.Registry {
	return effects.Registry{
		store.MessageInserted:  {activity, fx.msgCreated.Effect()},
		store.RoomInserted:     {fx.roomCreated.Effect()},
		store.EditInserted:     {activity, fx.editProjection.Effect(), fx.msgChanged.Effect()},
		store.ReactionChanged:  {activity, fx.reactionCounter.Effect(), fx.reactionEvent.Effect()},
		store.PinInserted:      {activity, fx.pinProjection.Effect(), fx.pinEvent.Effect()},
		store.MemberChanged:    {activity, fx.memberEvent.Effect(), fx.memberCountEvent.Effect()},
		store.ReadChanged:      {fx.readEvent.Effect()},
		store.MessageHidden:    {fx.hiddenEvent.Effect()},
		store.HistoryCleared:   {fx.historyCleared.Effect()},
		store.MemberCountCheck: {fx.memberCountRepair.Effect()},
	}
}

func (fx effectSet) counters() map[string]effectCounters {
	out := fx.memberCounters()
	maps.Copy(out, map[string]effectCounters{
		fx.msgCreated.Effect().Name:      {republished: fx.msgCreated.Republished, dropped: fx.msgCreated.Dropped},
		fx.roomCreated.Effect().Name:     {republished: fx.roomCreated.Republished, dropped: fx.roomCreated.Dropped},
		fx.msgChanged.Effect().Name:      {republished: fx.msgChanged.Republished, dropped: fx.msgChanged.Dropped},
		fx.editProjection.Effect().Name:  {dropped: fx.editProjection.Dropped},
		fx.reactionCounter.Effect().Name: {republished: fx.reactionCounter.Republished, dropped: fx.reactionCounter.Dropped},
		fx.reactionEvent.Effect().Name:   {republished: fx.reactionEvent.Republished, dropped: fx.reactionEvent.Dropped},
		fx.pinEvent.Effect().Name:        {republished: fx.pinEvent.Republished, dropped: fx.pinEvent.Dropped},
		fx.pinProjection.Effect().Name:   {dropped: fx.pinProjection.Dropped},
	})
	return out
}

func (fx effectSet) counterRepairs() map[string]func() uint64 {
	return map[string]func() uint64{
		pbconv.ReactionsCounter: fx.reactionCounter.Repaired,
		effects.MembersCounter:  fx.memberCountRepair.Repaired,
	}
}
