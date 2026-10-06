package main

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

type noMarks struct{}

func (noMarks) Acked(_ context.Context, keys []store.MsgKey) ([]bool, error) {
	return make([]bool, len(keys)), nil
}

func built[T any](v T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return v
	}
}

func TestEffectSetExportsEveryEffect(t *testing.T) {
	msgs, rooms, edits := memstore.NewMessages(), memstore.NewRooms(), memstore.NewEdits()
	reactions, pins, js := memstore.NewReactions(), memstore.NewPins(), &publishtest.JetStream{}
	events := effects.MessageChangedConfig{SubjectRoot: "evt"}
	fx := effectSet{
		msgCreated: built(effects.NewMessageCreated(
			effects.MessageCreatedDeps{Marks: noMarks{}, Messages: msgs, Rooms: rooms, JS: js}, effects.MessageCreatedConfig{SubjectRoot: "evt"}))(t),
		roomCreated:    built(effects.NewRoomCreated(effects.RoomCreatedDeps{Rooms: rooms, JS: js}, effects.RoomCreatedConfig{SubjectRoot: "evt"}))(t),
		editProjection: built(effects.NewEditProjection(effects.EditProjectionDeps{Edits: edits, Messages: msgs, Purger: edits}))(t),
		msgChanged:     built(effects.NewMessageChanged(effects.MessageChangedDeps{Edits: edits, Messages: msgs, Rooms: rooms, JS: js}, events))(t),
		reactionCounter: built(effects.NewReactionCounter(
			effects.ReactionCounterDeps{Messages: msgs, Counter: built(counter.New(msgs, reactions))(t), Rooms: rooms, JS: js},
			effects.ReactionCounterConfig{SubjectRoot: "evt"}))(t),
		reactionEvent: built(effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, Rooms: rooms, JS: js}, events))(t),
		pinProjection: built(effects.NewPinProjection(built(pinproj.New(pins, rooms))(t)))(t),
		pinEvent:      built(effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: msgs, Rooms: rooms, JS: js}, events))(t),
	}
	counters := fx.counters()
	want := []string{
		effects.EditProjectionName, effects.MessageChangedName, effects.MessageCreatedName, effects.PinEventName,
		effects.PinProjectionName, effects.ReactionCounterName, effects.ReactionEventName, effects.RoomCreatedName,
	}
	if got := slices.Sorted(maps.Keys(counters)); !slices.Equal(got, want) {
		t.Fatalf("effects with metrics = %v, want %v", got, want)
	}
	for name, c := range counters {
		silent := name == effects.EditProjectionName || name == effects.PinProjectionName
		if c.dropped == nil || (c.republished == nil) != silent {
			t.Errorf("%s: dropped set %v, republished set %v; want dropped always and republished only when it publishes", name, c.dropped != nil, c.republished != nil)
		}
	}
	if repairs := fx.counterRepairs(); len(repairs) != 1 || repairs[pbconv.ReactionsCounter] == nil {
		t.Errorf("counter repairs = %v, want only %q", repairs, pbconv.ReactionsCounter)
	}
}
