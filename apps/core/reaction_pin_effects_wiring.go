package main

import (
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func (fx *effectSet) wireReactionPinEffects(cfg config.Config, cl *clients, st *mongostore.Store) error {
	reactions, pins := st.Reactions(), st.Pins()
	counts, err := counter.New(st, reactions)
	if err != nil {
		return fmt.Errorf("wire reaction counter: %w", err)
	}
	projector, err := pinproj.New(pins, st)
	if err != nil {
		return fmt.Errorf("wire pin projector: %w", err)
	}
	events := effects.MessageChangedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache}
	if fx.reactionCounter, err = effects.NewReactionCounter(
		effects.ReactionCounterDeps{Messages: st, Counter: counts, Rooms: st, JS: cl.effectsJS},
		effects.ReactionCounterConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.ReactionCountDelay, RoomCache: cfg.EffectRoomCache},
	); err != nil {
		return fmt.Errorf("wire reaction_counter effect: %w", err)
	}
	if fx.reactionEvent, err = effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, Rooms: st, JS: cl.effectsJS}, events); err != nil {
		return fmt.Errorf("wire reaction_event effect: %w", err)
	}
	if fx.pinProjection, err = effects.NewPinProjection(projector); err != nil {
		return fmt.Errorf("wire pin_projection effect: %w", err)
	}
	if fx.pinEvent, err = effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: st, Rooms: st, JS: cl.effectsJS}, events); err != nil {
		return fmt.Errorf("wire pin_event effect: %w", err)
	}
	return nil
}
