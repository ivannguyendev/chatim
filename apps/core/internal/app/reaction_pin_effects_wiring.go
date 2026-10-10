package app

import (
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func (fx *effectSet) wireReactionPinEffects(cfg config.Config, cl *clients, st *mongostore.Store) error {
	interactions, pins := st.Interactions(), st.Pins()
	counts, err := counter.New(st, interactions)
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
	if fx.reactionEvent, err = effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: interactions, Rooms: st, JS: cl.effectsJS}, events); err != nil {
		return fmt.Errorf("wire reaction_event effect: %w", err)
	}
	if fx.bookmarkEvent, err = effects.NewBookmarkEvent(effects.BookmarkEventDeps{Bookmarks: interactions, Rooms: st, JS: cl.effectsJS}, events); err != nil {
		return fmt.Errorf("wire bookmark_event effect: %w", err)
	}
	if fx.pinProjection, err = effects.NewPinProjection(projector); err != nil {
		return fmt.Errorf("wire pin_projection effect: %w", err)
	}
	if fx.pinEvent, err = effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: st, Rooms: st, JS: cl.effectsJS}, events); err != nil {
		return fmt.Errorf("wire pin_event effect: %w", err)
	}
	return nil
}
