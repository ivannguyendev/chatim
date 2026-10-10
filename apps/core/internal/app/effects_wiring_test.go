package app

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
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
	reactions, pins, js := memstore.NewInteractions(), memstore.NewPins(), &publishtest.JetStream{}
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
		bookmarkEvent: built(effects.NewBookmarkEvent(effects.BookmarkEventDeps{Bookmarks: reactions, Rooms: rooms, JS: js}, events))(t),
	}
	withMemberEffects(t, &fx, rooms, js)
	fx.countRepair = built(effects.NewCountRepair(
		effects.CountRepairDeps{Messages: msgs, Interactions: reactions, Counts: msgs, Timers: noTimers{}, Rooms: rooms, JS: js},
		effects.CountRepairConfig{SubjectRoot: "evt"}))(t)
	counters := fx.counters()
	want := []string{
		effects.BookmarkEventName, effects.CountRepairName, effects.EditProjectionName, effects.HiddenEventName, effects.HistoryClearedEventName, effects.MemberCountEventName,
		effects.MemberCountRepairName, effects.MemberEventName, effects.MessageChangedName, effects.MessageCreatedName,
		effects.PinEventName, effects.PinProjectionName, effects.ReactionCounterName, effects.ReactionEventName,
		effects.ReadEventName, effects.RoomCreatedName,
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
	repairs := fx.counterRepairs()
	if len(repairs) != 3 || repairs[pbconv.ReactionsCounter] == nil || repairs[pbconv.RepliesCounter] == nil || repairs[effects.MembersCounter] == nil {
		t.Fatalf("counter repairs = %v, want %q, %q and %q", repairs, pbconv.ReactionsCounter, pbconv.RepliesCounter, effects.MembersCounter)
	}
	if repairs[pbconv.ReactionsCounter]() != 0 || repairs[pbconv.RepliesCounter]() != 0 {
		t.Errorf("fresh repair counters are not zero")
	}
	assertMemberRegistry(t, fx.registry(effects.NewRoomActivity(rooms).Effect()))
}

type noTimers struct{}

func (noTimers) ArmMemberCountCheck(context.Context, uint64) (work.Timer, error) {
	return work.Timer{}, nil
}

func (noTimers) ArmMessageCountCheck(context.Context, store.MsgKey, string) (work.Timer, error) {
	return work.Timer{}, nil
}

func withMemberEffects(t *testing.T, fx *effectSet, rooms *memstore.Rooms, js *publishtest.JetStream) {
	t.Helper()
	events := effects.MessageChangedConfig{SubjectRoot: "evt", Delay: 3 * time.Second}
	members := effects.MemberEventDeps{Members: rooms, Rooms: rooms, JS: js}
	fx.memberEvent = built(effects.NewMemberEvent(members, events))(t)
	fx.memberCountEvent = built(effects.NewMemberCountEvent(members, events))(t)
	fx.readEvent = built(effects.NewReadEvent(members, events))(t)
	fx.hiddenEvent = built(effects.NewHiddenEvent(effects.HiddenEventDeps{Hidden: memstore.NewHidden(), Rooms: rooms, JS: js}, events))(t)
	fx.historyCleared = built(effects.NewHistoryClearedEvent(members, events))(t)
	fx.memberCountRepair = built(effects.NewMemberCountRepair(
		effects.MemberCountRepairDeps{Rooms: rooms, Counts: rooms, Timers: noTimers{}, JS: js}, effects.MemberCountRepairConfig{SubjectRoot: "evt"}))(t)
}

func assertMemberRegistry(t *testing.T, reg effects.Registry) {
	t.Helper()
	want := map[store.ChangeKind][]string{
		store.MemberChanged:     {effects.RoomActivityName, effects.MemberEventName, effects.MemberCountEventName},
		store.ReadChanged:       {effects.ReadEventName},
		store.MessageHidden:     {effects.HiddenEventName},
		store.HistoryCleared:    {effects.HistoryClearedEventName},
		store.MemberCountCheck:  {effects.MemberCountRepairName},
		store.MessageCountCheck: {effects.CountRepairName},
		store.BookmarkChanged:   {effects.RoomActivityName, effects.BookmarkEventName},
	}
	for kind, names := range want {
		var got []string
		for i, e := range reg[kind] {
			got = append(got, e.Name)
			if i > 0 && e.Delay < reg[kind][i-1].Delay {
				t.Errorf("kind %d: %s delay %v runs after a longer delay %v", kind, e.Name, e.Delay, reg[kind][i-1].Delay)
			}
		}
		if !slices.Equal(got, names) {
			t.Errorf("kind %d effects = %v, want %v", kind, got, names)
		}
	}
	for _, kind := range []store.ChangeKind{store.MemberCountCheck, store.MessageCountCheck} {
		if d := reg[kind][0].Delay; d != 0 {
			t.Errorf("kind %d repair delay = %v, want 0 (the timer already waited)", kind, d)
		}
	}
	for kind := store.MessageInserted; kind <= store.MessageCountCheck; kind++ {
		if _, ok := reg[kind]; !ok {
			t.Errorf("kind %d has no effects; want every kind registered", kind)
		}
	}
}
