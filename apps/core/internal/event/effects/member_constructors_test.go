package effects_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type memberBuilder func(effects.MemberEventDeps, effects.MessageChangedConfig) error

func memberBuilders() map[string]memberBuilder {
	return map[string]memberBuilder{
		effects.MemberEventName: func(d effects.MemberEventDeps, c effects.MessageChangedConfig) error {
			_, err := effects.NewMemberEvent(d, c)
			return err
		},
		effects.MemberCountEventName: func(d effects.MemberEventDeps, c effects.MessageChangedConfig) error {
			_, err := effects.NewMemberCountEvent(d, c)
			return err
		},
		effects.ReadEventName: func(d effects.MemberEventDeps, c effects.MessageChangedConfig) error {
			_, err := effects.NewReadEvent(d, c)
			return err
		},
		effects.HistoryClearedEventName: func(d effects.MemberEventDeps, c effects.MessageChangedConfig) error {
			_, err := effects.NewHistoryClearedEvent(d, c)
			return err
		},
	}
}

func TestNewMemberEffectsRejectBadInput(t *testing.T) {
	js, mem, hidden := &publishtest.JetStream{}, memstore.NewRooms(), memstore.NewHidden()
	events, noRoot := effects.MessageChangedConfig{SubjectRoot: "evt"}, effects.MessageChangedConfig{}
	deps := effects.MemberEventDeps{Members: mem, Rooms: mem, JS: js}
	bad := map[string]func() error{}
	for name, build := range memberBuilders() {
		bad[name+" without members"] = func() error { return build(effects.MemberEventDeps{Rooms: mem, JS: js}, events) }
		bad[name+" without rooms"] = func() error { return build(effects.MemberEventDeps{Members: mem, JS: js}, events) }
		bad[name+" without js"] = func() error { return build(effects.MemberEventDeps{Members: mem, Rooms: mem}, events) }
		bad[name+" without a root"] = func() error { return build(deps, noRoot) }
		if err := build(deps, events); err != nil {
			t.Errorf("%s with defaults: %v", name, err)
		}
	}
	hide := func(d effects.HiddenEventDeps, c effects.MessageChangedConfig) error {
		_, err := effects.NewHiddenEvent(d, c)
		return err
	}
	bad["hidden_event without hidden"] = func() error { return hide(effects.HiddenEventDeps{Rooms: mem, JS: js}, events) }
	bad["hidden_event without rooms"] = func() error { return hide(effects.HiddenEventDeps{Hidden: hidden, JS: js}, events) }
	bad["hidden_event without js"] = func() error { return hide(effects.HiddenEventDeps{Hidden: hidden, Rooms: mem}, events) }
	bad["hidden_event without a root"] = func() error { return hide(effects.HiddenEventDeps{Hidden: hidden, Rooms: mem, JS: js}, noRoot) }
	for name, build := range bad {
		if err := build(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
	if err := hide(effects.HiddenEventDeps{Hidden: hidden, Rooms: mem, JS: js}, events); err != nil {
		t.Errorf("hidden_event with defaults: %v", err)
	}
}
