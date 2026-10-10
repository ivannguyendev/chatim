package effects_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNewReactionAndPinEffectsRejectBadInput(t *testing.T) {
	js, msgs, mem, reactions, pins := &publishtest.JetStream{}, memstore.NewMessages(), memstore.NewRooms(), memstore.NewInteractions(), memstore.NewPins()
	counts, events, noRoot := &spyCounter{}, effects.MessageChangedConfig{SubjectRoot: "evt"}, effects.MessageChangedConfig{}
	count := effects.ReactionCounterConfig{SubjectRoot: "evt"}
	counterDeps := effects.ReactionCounterDeps{Messages: msgs, Counter: counts, Rooms: mem, JS: js}
	eventDeps := effects.ReactionEventDeps{Reactions: reactions, Rooms: mem, JS: js}
	pinDeps := effects.PinEventDeps{Pins: pins, Messages: msgs, Rooms: mem, JS: js}
	bad := map[string]func() error{
		"reaction_event without reactions": func() error {
			_, err := effects.NewReactionEvent(effects.ReactionEventDeps{Rooms: mem, JS: js}, events)
			return err
		},
		"reaction_event without rooms": func() error {
			_, err := effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, JS: js}, events)
			return err
		},
		"reaction_event without js": func() error {
			_, err := effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, Rooms: mem}, events)
			return err
		},
		"reaction_event without a root": func() error { _, err := effects.NewReactionEvent(eventDeps, noRoot); return err },
		"reaction_counter without messages": func() error {
			_, err := effects.NewReactionCounter(effects.ReactionCounterDeps{Counter: counts, Rooms: mem, JS: js}, count)
			return err
		},
		"reaction_counter without a counter": func() error {
			_, err := effects.NewReactionCounter(effects.ReactionCounterDeps{Messages: msgs, Rooms: mem, JS: js}, count)
			return err
		},
		"reaction_counter without rooms": func() error {
			_, err := effects.NewReactionCounter(effects.ReactionCounterDeps{Messages: msgs, Counter: counts, JS: js}, count)
			return err
		},
		"reaction_counter without js": func() error {
			_, err := effects.NewReactionCounter(effects.ReactionCounterDeps{Messages: msgs, Counter: counts, Rooms: mem}, count)
			return err
		},
		"reaction_counter without a root": func() error {
			_, err := effects.NewReactionCounter(counterDeps, effects.ReactionCounterConfig{})
			return err
		},
		"reaction_counter with negative tries": func() error {
			_, err := effects.NewReactionCounter(counterDeps, effects.ReactionCounterConfig{SubjectRoot: "evt", Tries: -1})
			return err
		},
		"pin_projection without a projector": func() error { _, err := effects.NewPinProjection(nil); return err },
		"pin_event without pins": func() error {
			_, err := effects.NewPinEvent(effects.PinEventDeps{Messages: msgs, Rooms: mem, JS: js}, events)
			return err
		},
		"pin_event without messages": func() error {
			_, err := effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Rooms: mem, JS: js}, events)
			return err
		},
		"pin_event without rooms": func() error {
			_, err := effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: msgs, JS: js}, events)
			return err
		},
		"pin_event without js": func() error {
			_, err := effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: msgs, Rooms: mem}, events)
			return err
		},
		"pin_event without a root": func() error { _, err := effects.NewPinEvent(pinDeps, noRoot); return err },
	}
	for name, build := range bad {
		if err := build(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s = %v, want ErrInvalidArgument", name, err)
		}
	}
	ctr, err := effects.NewReactionCounter(counterDeps, count)
	if err != nil || ctr.Effect().Delay != effects.DefaultCountDelay {
		t.Fatalf("reaction_counter with defaults = %v, %v; want delay %v", ctr, err, effects.DefaultCountDelay)
	}
	ev, err := effects.NewReactionEvent(eventDeps, events)
	if err != nil || ev.Effect().Delay != effects.DefaultDelay {
		t.Fatalf("reaction_event with defaults = %v, %v; want delay %v", ev, err, effects.DefaultDelay)
	}
	pe, err := effects.NewPinEvent(pinDeps, events)
	if err != nil || pe.Effect().Delay != effects.DefaultDelay {
		t.Fatalf("pin_event with defaults = %v, %v; want delay %v", pe, err, effects.DefaultDelay)
	}
}
