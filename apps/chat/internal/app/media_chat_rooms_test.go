package app

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store/memstore"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	avatarGroup  = uint64(9101)
	avatarDirect = uint64(9102)
	groupAvatar  = "acme/alice/pub/20261107/0123456789abcdefghijklmnop"
)

func seedAvatarRooms(t *testing.T, rooms store.Rooms) {
	t.Helper()
	owner := domain.Member{Room: avatarGroup, Tenant: mediaTenant, User: "alice", Role: domain.RoleOwner, JoinedAt: mediaSent}
	group := domain.Room{ID: avatarGroup, Tenant: mediaTenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: mediaSent, MemberCount: 1}
	if err := rooms.Create(t.Context(), group, []domain.Member{owner}); err != nil {
		t.Fatalf("Create group: %v", err)
	}
	dm, members, err := domain.NewDirectRoom(mediaTenant, "alice", "bob", mediaSent, avatarDirect)
	if err != nil {
		t.Fatal(err)
	}
	if err := rooms.Create(t.Context(), dm, members); err != nil {
		t.Fatalf("Create direct: %v", err)
	}
}

type avatarRooms interface {
	store.Rooms
	store.RoomUpdater
}

func setGroupAvatar(t *testing.T, rooms avatarRooms) uint64 {
	t.Helper()
	r, err := rooms.Get(t.Context(), avatarGroup)
	if err != nil {
		t.Fatal(err)
	}
	change := store.RoomInfoChange{Update: domain.RoomUpdate{Avatars: new([]string{groupAvatar})}, By: "alice", At: mediaSent}
	if ok, err := rooms.UpdateRoomInfo(t.Context(), avatarGroup, r.InfoVer, change); err != nil || !ok {
		t.Fatalf("UpdateRoomInfo = %v, %v", ok, err)
	}
	return r.InfoVer + 1
}

func assertRoomAvatars(t *testing.T, node mediaRoomAvatars, ver uint64) {
	t.Helper()
	for room, want := range map[string]mediaapp.RoomAvatarState{
		"9101": {Found: true, Tenant: mediaTenant, Group: true, Keys: []string{groupAvatar}, InfoVer: ver},
		"9102": {Found: true, Tenant: mediaTenant, InfoVer: 1},
		"9103": {},
	} {
		got, err := node.RoomAvatars(t.Context(), room)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("RoomAvatars(%s) = %+v, %v; want %+v", room, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "0"} {
		if _, err := node.RoomAvatars(t.Context(), bad); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("RoomAvatars(%q) = %v, want InvalidArgument", bad, err)
		}
	}
}

func TestMediaRoomAvatarsReadsTheStoredRoom(t *testing.T) {
	rooms := memstore.NewRooms()
	seedAvatarRooms(t, rooms)
	assertRoomAvatars(t, mediaRoomAvatars{rooms: rooms}, setGroupAvatar(t, rooms))
}
