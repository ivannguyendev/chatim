package app

import (
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func (fx *effectSet) wireMemberEffects(cfg config.Config, cl *clients, st *mongostore.Store, timers effects.CountTimers) error {
	events := effects.MessageChangedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache}
	members := effects.MemberEventDeps{Members: st, Rooms: st, JS: cl.effectsJS}
	var err error
	if fx.memberEvent, err = effects.NewMemberEvent(members, events); err != nil {
		return fmt.Errorf("wire member_event effect: %w", err)
	}
	if fx.memberCountEvent, err = effects.NewMemberCountEvent(members, events); err != nil {
		return fmt.Errorf("wire member_count_event effect: %w", err)
	}
	if fx.readEvent, err = effects.NewReadEvent(members, events); err != nil {
		return fmt.Errorf("wire read_event effect: %w", err)
	}
	if fx.hiddenEvent, err = effects.NewHiddenEvent(effects.HiddenEventDeps{Hidden: st.Hidden(), Rooms: st, JS: cl.effectsJS}, events); err != nil {
		return fmt.Errorf("wire hidden_event effect: %w", err)
	}
	if fx.historyCleared, err = effects.NewHistoryClearedEvent(members, events); err != nil {
		return fmt.Errorf("wire history_cleared_event effect: %w", err)
	}
	if fx.memberCountRepair, err = effects.NewMemberCountRepair(
		effects.MemberCountRepairDeps{Rooms: st, Counts: st, Timers: timers, JS: cl.effectsJS},
		effects.MemberCountRepairConfig{SubjectRoot: cfg.Stream.SubjectRoot},
	); err != nil {
		return fmt.Errorf("wire member_count_repair effect: %w", err)
	}
	return nil
}

func (fx effectSet) memberCounters() map[string]effectCounters {
	return map[string]effectCounters{
		fx.memberEvent.Effect().Name:       {republished: fx.memberEvent.Republished, dropped: fx.memberEvent.Dropped},
		fx.memberCountEvent.Effect().Name:  {republished: fx.memberCountEvent.Republished, dropped: fx.memberCountEvent.Dropped},
		fx.readEvent.Effect().Name:         {republished: fx.readEvent.Republished, dropped: fx.readEvent.Dropped},
		fx.hiddenEvent.Effect().Name:       {republished: fx.hiddenEvent.Republished, dropped: fx.hiddenEvent.Dropped},
		fx.historyCleared.Effect().Name:    {republished: fx.historyCleared.Republished, dropped: fx.historyCleared.Dropped},
		fx.memberCountRepair.Effect().Name: {republished: fx.memberCountRepair.Republished, dropped: fx.memberCountRepair.Dropped},
	}
}
