package room_test

import (
	"context"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/chat/internal/room"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

type lostInfoCAS struct {
	*memstore.Rooms
	err error
}

func (l lostInfoCAS) UpdateRoomInfo(context.Context, uint64, uint64, store.RoomInfoChange) (bool, error) {
	return false, l.err
}

var avatarKeyCode = base32.NewEncoding("0123456789abcdefghijklmnopqrstuv").WithPadding(base32.NoPadding)

func avatarKey(user string, at time.Time, seed byte) string {
	var raw [16]byte
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(at.UnixMilli()))
	copy(raw[:6], ms[2:])
	for i := 6; i < 16; i++ {
		raw[i] = seed + byte(i)
	}
	return tenant + "/" + user + "/pub/" + at.UTC().Format("20060102") + "/" + avatarKeyCode.EncodeToString(raw[:])
}

func rename(user string, r uint64, name *string) room.UpdateRoomCmd {
	return room.UpdateRoomCmd{Tenant: tenant, User: user, Room: r, Update: domain.RoomUpdate{Name: name}}
}

func setAvatars(user string, r uint64, name *string, avatars ...string) room.UpdateRoomCmd {
	return room.UpdateRoomCmd{Tenant: tenant, User: user, Room: r, Update: domain.RoomUpdate{Name: name, Avatars: &avatars}}
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
	res, err := rg.m.UpdateRoom(t.Context(), rename("owen", group, new("Design")))
	if err != nil || !res.Changed || res.Room.Name != "Design" || res.Room.InfoVer != before.InfoVer+1 {
		t.Fatalf("owner UpdateRoom = %+v, %v; want Design at info_ver %d", res, err, before.InfoVer+1)
	}
	key := avatarKey("ada", rg.now.Add(-time.Minute), 1)
	res, err = rg.m.UpdateRoom(t.Context(), setAvatars("ada", group, nil, key))
	if err != nil || !res.Changed || !slices.Equal(res.Room.Avatars, []string{key}) || res.Room.Name != "Design" || res.Room.UpdatedBy != "ada" {
		t.Fatalf("admin UpdateRoom = %+v, %v; want the avatar set by ada", res, err)
	}
	stored := rg.roomOf(t, group)
	if stored.Name != "Design" || !slices.Equal(stored.Avatars, []string{key}) || stored.InfoVer != before.InfoVer+2 || !stored.UpdatedAt.Equal(rg.at()) {
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
	if last := events[1].GetRoomUpdated().GetRoom(); last.GetName() != "Design" || !slices.Equal(last.GetAvatars(), []string{key}) || events[1].GetActor() != "ada" {
		t.Fatalf("last room_updated = %v, want the full room after ada's change", events[1])
	}
}

func TestMembersCannotUpdateTheRoomInfo(t *testing.T) {
	rg := newMemberRig(t, nil)
	before := rg.roomOf(t, group)
	if _, err := rg.m.UpdateRoom(t.Context(), rename("mia", group, new("Mine"))); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("member UpdateRoom = %v, want PermissionDenied", err)
	}
	if _, err := rg.m.UpdateRoom(t.Context(), rename("nia", group, new("Mine"))); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("outsider UpdateRoom = %v, want PermissionDenied", err)
	}
	other := room.UpdateRoomCmd{Tenant: "other", User: "owen", Room: group, Update: domain.RoomUpdate{Name: new("Mine")}}
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
	if _, err := rg.m.UpdateRoom(t.Context(), rename("mia", direct, new("Us"))); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("UpdateRoom of a direct room = %v, want FailedPrecondition", err)
	}
}

func TestAnUpdateThatChangesNothingWritesNothing(t *testing.T) {
	rg := newMemberRig(t, nil)
	before := rg.roomOf(t, group)
	res, err := rg.m.UpdateRoom(t.Context(), setAvatars("owen", group, new(before.Name)))
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

func TestEmptyAvatarsRemoveTheAvatar(t *testing.T) {
	rg := newMemberRig(t, nil)
	if _, err := rg.m.UpdateRoom(t.Context(), setAvatars("owen", group, nil, avatarKey("owen", rg.now, 1))); err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	res, err := rg.m.UpdateRoom(t.Context(), setAvatars("owen", group, nil))
	if err != nil || !res.Changed || res.Room.Avatars != nil || rg.roomOf(t, group).Avatars != nil {
		t.Fatalf("remove avatar = %+v, %v; want the avatar gone", res, err)
	}
}

func TestAvatarKeysMustBeTheCallersFreshPublicFiles(t *testing.T) {
	rg := newMemberRig(t, nil)
	before := rg.roomOf(t, group)
	private := avatarKey("owen", rg.now, 1)
	for name, key := range map[string]string{
		"someone else's file": avatarKey("ada", rg.now, 1),
		"private file":        private[:len(tenant+"/owen/")] + private[len(tenant+"/owen/pub/"):],
		"25 hours old":        avatarKey("owen", rg.now.Add(-25*time.Hour), 1),
	} {
		if _, err := rg.m.UpdateRoom(t.Context(), setAvatars("owen", group, nil, key)); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: UpdateRoom = %v, want InvalidArgument", name, err)
		}
	}
	if after := rg.roomOf(t, group); after.Avatars != nil || after.InfoVer != before.InfoVer {
		t.Fatalf("room = %+v after refused avatars, want %+v", after, before)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("events = %v, want none", events)
	}
}

func TestTheCurrentAvatarIsKeptAndNotCheckedAgain(t *testing.T) {
	rg := newMemberRig(t, nil)
	key := avatarKey("owen", rg.now, 1)
	if _, err := rg.m.UpdateRoom(t.Context(), setAvatars("owen", group, nil, key)); err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	rg.now = rg.now.Add(25 * time.Hour)
	res, err := rg.m.UpdateRoom(t.Context(), rename("ada", group, new("Kho")))
	if err != nil || !res.Changed || !slices.Equal(res.Room.Avatars, []string{key}) {
		t.Fatalf("rename = %+v, %v; want the avatar kept", res, err)
	}
	res, err = rg.m.UpdateRoom(t.Context(), setAvatars("ada", group, new("Kho 2"), key))
	if err != nil || !res.Changed || res.Room.Name != "Kho 2" || !slices.Equal(rg.roomOf(t, group).Avatars, []string{key}) {
		t.Fatalf("rename with the current avatar = %+v, %v; want accepted", res, err)
	}
}

func TestBadRoomUpdatesAreRefusedBeforeAnyRead(t *testing.T) {
	rg := newMemberRig(t, nil)
	for name, c := range map[string]room.UpdateRoomCmd{
		"nothing":     rename("nia", group, nil),
		"empty name":  rename("nia", group, new("")),
		"two avatars": setAvatars("nia", group, nil, "a", "b"),
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
		_, err := rg.build(t, d).UpdateRoom(t.Context(), rename("owen", group, new("Design")))
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
