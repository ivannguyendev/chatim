package mutate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type lostInfoCAS struct {
	*memstore.Rooms
	err error
}

func (l lostInfoCAS) UpdateRoomInfo(context.Context, uint64, uint64, store.RoomInfoChange) (bool, error) {
	return false, l.err
}

func rename(user string, r uint64, name, avatar *string) mutate.UpdateRoomCmd {
	return mutate.UpdateRoomCmd{Tenant: tenant, User: user, Room: r, Update: domain.RoomUpdate{Name: name, Avatar: avatar}}
}

func (rg *rig) roomOf(t *testing.T, r uint64) domain.Room {
	t.Helper()
	got, err := rg.rooms.Get(t.Context(), r)
	if err != nil {
		t.Fatalf("get room %d: %v", r, err)
	}
	return got
}

func TestOwnerAndAdminUpdateTheRoomInfoAndEachChangeSendsRoomUpdated(t *testing.T) {
	rg := newMemberRig(t, nil)
	before := rg.roomOf(t, group)
	res, err := rg.m.UpdateRoom(t.Context(), rename("owen", group, new("Design"), nil))
	if err != nil || !res.Changed || res.Room.Name != "Design" || res.Room.InfoVer != before.InfoVer+1 {
		t.Fatalf("owner UpdateRoom = %+v, %v; want Design at info_ver %d", res, err, before.InfoVer+1)
	}
	res, err = rg.m.UpdateRoom(t.Context(), rename("ada", group, nil, new("files/av/1.png")))
	if err != nil || !res.Changed || res.Room.Avatar != "files/av/1.png" || res.Room.Name != "Design" || res.Room.UpdatedBy != "ada" {
		t.Fatalf("admin UpdateRoom = %+v, %v; want the avatar set by ada", res, err)
	}
	stored := rg.roomOf(t, group)
	if stored.Name != "Design" || stored.Avatar != "files/av/1.png" || stored.InfoVer != before.InfoVer+2 || !stored.UpdatedAt.Equal(rg.at()) {
		t.Fatalf("stored room = %+v, want both changes at info_ver %d", stored, before.InfoVer+2)
	}
	rooms, events := rg.events.list()
	if len(events) != 2 || rooms[0] != group || rooms[1] != group {
		t.Fatalf("events = %v in rooms %v, want two room_updated in the group", events, rooms)
	}
	for i, ev := range events {
		ver := before.InfoVer + uint64(i) + 1
		if ev.GetId() != pbconv.RoomInfoEventID(group, ver) || ev.GetRoomUpdated().GetRoom().GetInfoVer() != ver {
			t.Fatalf("event %d = %v, want room_updated at info_ver %d", i, ev, ver)
		}
	}
	if last := events[1].GetRoomUpdated().GetRoom(); last.GetName() != "Design" || last.GetAvatar() != "files/av/1.png" || events[1].GetActor() != "ada" {
		t.Fatalf("last room_updated = %v, want the full room after ada's change", events[1])
	}
}

func TestMembersCannotUpdateTheRoomInfo(t *testing.T) {
	rg := newMemberRig(t, nil)
	before := rg.roomOf(t, group)
	if _, err := rg.m.UpdateRoom(t.Context(), rename("mia", group, new("Mine"), nil)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("member UpdateRoom = %v, want PermissionDenied", err)
	}
	if _, err := rg.m.UpdateRoom(t.Context(), rename("nia", group, new("Mine"), nil)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("outsider UpdateRoom = %v, want PermissionDenied", err)
	}
	other := mutate.UpdateRoomCmd{Tenant: "other", User: "owen", Room: group, Update: domain.RoomUpdate{Name: new("Mine")}}
	if _, err := rg.m.UpdateRoom(t.Context(), other); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("other tenant UpdateRoom = %v, want NotFound", err)
	}
	if after := rg.roomOf(t, group); after.Name != before.Name || after.InfoVer != before.InfoVer {
		t.Fatalf("room = %+v after refused updates, want %+v", after, before)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("events = %v, want none", events)
	}
}

func TestDirectRoomInfoIsRefusedBeforeThePolicy(t *testing.T) {
	rg := newRig(t, nil)
	r, members, err := domain.NewDirectRoom(tenant, "owen", "mia", created, direct)
	if err != nil {
		t.Fatalf("NewDirectRoom: %v", err)
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create direct: %v", err)
	}
	if _, err := rg.m.UpdateRoom(t.Context(), rename("mia", direct, new("Us"), nil)); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("UpdateRoom of a direct room = %v, want FailedPrecondition", err)
	}
}

func TestAnUpdateThatChangesNothingWritesNothing(t *testing.T) {
	rg := newMemberRig(t, nil)
	before := rg.roomOf(t, group)
	res, err := rg.m.UpdateRoom(t.Context(), rename("owen", group, new(before.Name), new("")))
	if err != nil || res.Changed || res.Room.InfoVer != before.InfoVer || res.Room.Name != before.Name {
		t.Fatalf("UpdateRoom with the current info = %+v, %v; want unchanged at info_ver %d", res, err, before.InfoVer)
	}
	if after := rg.roomOf(t, group); after.InfoVer != before.InfoVer {
		t.Fatalf("info_ver = %d, want %d", after.InfoVer, before.InfoVer)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("events = %v, want none", events)
	}
}

func TestAnEmptyAvatarRemovesTheAvatar(t *testing.T) {
	rg := newMemberRig(t, nil)
	if _, err := rg.m.UpdateRoom(t.Context(), rename("owen", group, nil, new("files/av/1.png"))); err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	res, err := rg.m.UpdateRoom(t.Context(), rename("owen", group, nil, new("")))
	if err != nil || !res.Changed || res.Room.Avatar != "" || rg.roomOf(t, group).Avatar != "" {
		t.Fatalf("remove avatar = %+v, %v; want the avatar gone", res, err)
	}
}

func TestBadRoomUpdatesAreRefusedBeforeAnyRead(t *testing.T) {
	rg := newMemberRig(t, nil)
	for name, c := range map[string]mutate.UpdateRoomCmd{
		"nothing":     rename("nia", group, nil, nil),
		"empty name":  rename("nia", group, new(""), nil),
		"long avatar": rename("nia", group, nil, new(strings.Repeat("a", 1025))),
	} {
		if _, err := rg.m.UpdateRoom(t.Context(), c); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: UpdateRoom = %v, want InvalidArgument", name, err)
		}
	}
}

func TestALostInfoCASAsksTheClientToRetryAtOnce(t *testing.T) {
	rg := newMemberRig(t, nil)
	for name, spy := range map[string]lostInfoCAS{"lost": {Rooms: rg.rooms}, "failed": {Rooms: rg.rooms, err: errBoom}} {
		d := rg.deps(t, nil)
		d.Rooms = spy
		_, err := rg.build(t, d).UpdateRoom(t.Context(), rename("owen", group, new("Design"), nil))
		want := domain.ErrRetryLater
		if spy.err != nil {
			want = errBoom
		}
		if !errors.Is(err, want) {
			t.Fatalf("%s: UpdateRoom = %v, want %v", name, err, want)
		}
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("events = %v, want none", events)
	}
}
