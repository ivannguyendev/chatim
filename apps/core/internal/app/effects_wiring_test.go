package app

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
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
		countEvent:     built(effects.NewCountEvent(effects.CountEventDeps{Messages: msgs, Rooms: rooms, JS: js}, events))(t),
		reactionEvent:  built(effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, Rooms: rooms, JS: js}, events))(t),
		pinProjection:  built(effects.NewPinProjection(built(pinproj.New(pins, rooms))(t)))(t),
		pinEvent:       built(effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: msgs, Rooms: rooms, JS: js}, events))(t),
		bookmarkEvent:  built(effects.NewBookmarkEvent(effects.BookmarkEventDeps{Bookmarks: reactions, Rooms: rooms, JS: js}, events))(t),
	}
	withMemberEffects(t, &fx, rooms, js)
	fx.replyMentionIndex = built(effects.NewReplyMentionIndex(
		effects.ReplyMentionIndexDeps{Messages: msgs, Replies: reactions, Threads: memstore.NewReplyThreads(), Counts: msgs, Mentions: memstore.NewMentions(), Shared: rooms, Timers: noTimers{}, Rooms: rooms, JS: js},
		effects.ReplyMentionIndexConfig{SubjectRoot: "evt"}))(t)
	counters := fx.counters()
	want := []string{
		effects.BookmarkEventName, effects.CountEventName, effects.EditProjectionName, effects.HiddenEventName, effects.HistoryClearedEventName, effects.MemberCountEventName,
		effects.MemberEventName, effects.MentionReadName, effects.MessageChangedName, effects.MessageCreatedName,
		effects.PinEventName, effects.PinProjectionName, effects.PreferencesEventName, effects.ReactionEventName,
		effects.ReadEventName, effects.ReplyMentionIndexName, effects.RoomCreatedName, effects.RoomEventName,
	}
	if got := slices.Sorted(maps.Keys(counters)); !slices.Equal(got, want) {
		t.Fatalf("effects with metrics = %v, want %v", got, want)
	}
	for name, c := range counters {
		silent := name == effects.EditProjectionName || name == effects.PinProjectionName || name == effects.MentionReadName
		if c.dropped == nil || (c.republished == nil) != silent {
			t.Errorf("%s: dropped set %v, republished set %v; want dropped always and republished only when it publishes", name, c.dropped != nil, c.republished != nil)
		}
	}
	assertMemberRegistry(t, fx.registry(effects.NewRoomActivity(rooms).Effect()))
}

type noTimers struct{}

func (noTimers) ArmMessageCountCheck(context.Context, store.MsgKey, string) (timer.Timer, error) {
	return timer.Timer{}, nil
}

func (noTimers) Disarm(context.Context, timer.Timer) {}

func withMemberEffects(t *testing.T, fx *effectSet, rooms *memstore.Rooms, js *publishtest.JetStream) {
	t.Helper()
	events := effects.MessageChangedConfig{SubjectRoot: "evt", Delay: 3 * time.Second}
	members := effects.MemberEventDeps{Members: rooms, Rooms: rooms, JS: js}
	fx.memberEvent = built(effects.NewMemberEvent(members, events))(t)
	fx.memberCountEvent = built(effects.NewMemberCountEvent(members, events))(t)
	fx.readEvent = built(effects.NewReadEvent(members, events))(t)
	fx.mentionRead = built(effects.NewMentionRead(effects.MentionReadDeps{Members: rooms, Mentions: memstore.NewMentions()}))(t)
	fx.hiddenEvent = built(effects.NewHiddenEvent(effects.HiddenEventDeps{Hidden: memstore.NewHidden(), Rooms: rooms, JS: js}, events))(t)
	fx.historyCleared = built(effects.NewHistoryClearedEvent(members, events))(t)
	fx.preferencesEvent = built(effects.NewPreferencesEvent(members, events))(t)
	fx.roomEvent = built(effects.NewRoomEvent(effects.RoomEventDeps{Rooms: rooms, JS: js}, events))(t)
}

func assertMemberRegistry(t *testing.T, reg effects.Registry) {
	t.Helper()
	want := map[store.ChangeKind][]string{
		store.MemberChanged:     {effects.RoomActivityName, effects.MemberEventName, effects.MemberCountEventName},
		store.ReadChanged:       {effects.MentionReadName, effects.ReadEventName},
		store.MessageHidden:     {effects.HiddenEventName},
		store.HistoryCleared:    {effects.HistoryClearedEventName},
		store.ReactionChanged:   {effects.RoomActivityName, effects.ReactionEventName, effects.CountEventName},
		store.BookmarkChanged:   {effects.RoomActivityName, effects.BookmarkEventName},
		store.MessageInserted:   {effects.RoomActivityName, effects.ReplyMentionIndexName, effects.MessageCreatedName},
		store.EditInserted:      {effects.RoomActivityName, effects.EditProjectionName, effects.ReplyMentionIndexName, effects.MessageChangedName},
		store.RoomChanged:       {effects.RoomActivityName, effects.RoomEventName},
		store.PreferenceChanged: {effects.RoomActivityName, effects.PreferencesEventName},
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
		if effs, ok := reg[kind]; ok {
			t.Errorf("kind %d has effects %v; count checks fire on the timer stream now", kind, effs)
		}
	}
	for kind := store.MessageInserted; kind <= store.PreferenceChanged; kind++ {
		if _, ok := reg[kind]; !ok && kind != store.MemberCountCheck && kind != store.MessageCountCheck {
			t.Errorf("kind %d has no effects; want every kind registered", kind)
		}
	}
}
