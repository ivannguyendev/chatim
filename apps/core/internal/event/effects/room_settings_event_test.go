package effects_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func settingsEffects(t *testing.T, rg *memberRig) (*effects.RoomEvent, *effects.PreferencesEvent) {
	t.Helper()
	events := effects.MessageChangedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16}
	info := built(effects.NewRoomEvent(effects.RoomEventDeps{Rooms: rg.rooms, JS: rg.js}, events))(t)
	members := effects.MemberEventDeps{Members: rg.rooms, Rooms: rg.rooms, JS: rg.js, Now: func() time.Time { return memberNow }}
	return info, built(effects.NewPreferencesEvent(members, events))(t)
}

func rename(t *testing.T, rg *memberRig, base uint64, name string) {
	t.Helper()
	c := store.RoomInfoChange{Update: domain.RoomUpdate{Name: new(name)}, By: "alice", At: memberNow}
	if ok, err := rg.rooms.UpdateRoomInfo(t.Context(), room, base, c); err != nil || !ok {
		t.Fatalf("UpdateRoomInfo(base %d) = %v, %v", base, ok, err)
	}
}

func pin(t *testing.T, rg *memberRig, on bool) domain.RoomPreferences {
	t.Helper()
	p, changed, err := rg.rooms.SetPreferences(t.Context(), room, "alice", domain.PreferencesChange{Pinned: new(on)}, memberNow)
	if err != nil || !changed {
		t.Fatalf("SetPreferences(pinned %v) = %+v, %v, %v", on, p, changed, err)
	}
	return p
}

func infoRec(ver uint32) work.Record {
	return work.Record{Kind: store.RoomChanged, Room: room, Version: ver, CommittedAt: memberNow}
}

func preferenceRec(user string, ver uint32) work.Record {
	return work.Record{Kind: store.PreferenceChanged, Room: room, User: user, Version: ver, CommittedAt: memberNow}
}

func TestRoomEventRepublishesTheRoomAtTheRecordInfoVerOnce(t *testing.T) {
	rg := newMemberRig(t)
	info, _ := settingsEffects(t, rg)
	if e := info.Effect(); e.Name != effects.RoomEventName || e.Delay != delay {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.RoomEventName, delay)
	}
	rename(t, rg, 1, "Kho")
	for range 2 {
		if errs := info.Effect().Run(t.Context(), []work.Record{infoRec(2)}); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.RoomInfoEventID(room, 2)}) || rg.js.Stored()[0].Subject != "evt.acme.room.4242.room_updated" {
		t.Fatalf("stored = %v, want one room_updated on the room subject", got)
	}
	current, _ := rg.rooms.Get(t.Context(), room)
	if events, err := rg.js.Events(); err != nil || !proto.Equal(events[0], pbconv.RoomUpdated(current)) || current.Name != "Kho" {
		t.Fatalf("event = %v, %v; want the current room", events, err)
	}
	if info.Republished() != 1 || info.Dropped() != 0 {
		t.Fatalf("republished %d dropped %d; want 1 (the duplicate PubAck is not counted) and 0", info.Republished(), info.Dropped())
	}
}

func TestRoomEventSkipsANewerRoomRetriesAnOlderOneAndDropsAMissingOne(t *testing.T) {
	rg := newMemberRig(t)
	info, _ := settingsEffects(t, rg)
	rename(t, rg, 1, "Kho")
	rename(t, rg, 2, "Kho mới")
	missing := work.Record{Kind: store.RoomChanged, Room: 9999, Version: 2, CommittedAt: memberNow}
	errs := info.Effect().Run(t.Context(), []work.Record{infoRec(2), infoRec(4), missing})
	if len(errs) != 3 || errs[0] != nil || !errors.Is(errs[1], store.ErrStaleRead) || errs[2] != nil {
		t.Fatalf("errs = %v, want only the record ahead of the read retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || info.Dropped() != 1 {
		t.Fatalf("attempts %d dropped %d; want nothing sent and the missing room dropped", len(rg.js.Attempts()), info.Dropped())
	}
	if _, err := effects.NewRoomEvent(effects.RoomEventDeps{}, effects.MessageChangedConfig{SubjectRoot: "evt"}); err == nil {
		t.Fatal("NewRoomEvent without deps = nil error")
	}
}

func TestPreferencesEventRepublishesTheFullStateAtTheRecordVer(t *testing.T) {
	rg := newMemberRig(t)
	_, prefs := settingsEffects(t, rg)
	if e := prefs.Effect(); e.Name != effects.PreferencesEventName || e.Delay != delay {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.PreferencesEventName, delay)
	}
	p := pin(t, rg, true)
	if errs := prefs.Effect().Run(t.Context(), []work.Record{preferenceRec("alice", uint32(p.Ver))}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.PreferencesEventID(room, "alice", 1)}) || rg.js.Stored()[0].Subject != "evt.acme.member.4242.room_preferences_changed" {
		t.Fatalf("stored = %v, want one room_preferences_changed on the member subject", got)
	}
	doc, _ := rg.rooms.MembersOf(t.Context(), room, []string{"alice"})
	if events, err := rg.js.Events(); err != nil || !proto.Equal(events[0], pbconv.RoomPreferencesChanged(rg.room, doc[0], memberNow)) {
		t.Fatalf("event = %v, %v; want the current state of alice", events, err)
	}
	if prefs.Republished() != 1 {
		t.Fatalf("republished %d, want 1", prefs.Republished())
	}
}

func TestPreferencesEventSkipsANewerStateRetriesAnOlderOneAndDropsAMissingMember(t *testing.T) {
	rg := newMemberRig(t)
	_, prefs := settingsEffects(t, rg)
	pin(t, rg, true)
	pin(t, rg, false)
	errs := prefs.Effect().Run(t.Context(), []work.Record{preferenceRec("alice", 1), preferenceRec("alice", 3), preferenceRec("carol", 1)})
	if len(errs) != 3 || errs[0] != nil || !errors.Is(errs[1], store.ErrStaleRead) || errs[2] != nil {
		t.Fatalf("errs = %v, want only the record ahead of the read retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || prefs.Dropped() != 1 {
		t.Fatalf("attempts %d dropped %d; want nothing sent and the missing member dropped", len(rg.js.Attempts()), prefs.Dropped())
	}
}
