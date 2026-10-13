package grpcsrv_test

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestUpdateRoomThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	rg := newRig(t, options{events: events})
	room := rg.createGroup(t, "acme", "alice", "bob", "carol")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	if _, err := rg.client.ChangeMemberRole(alice, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN}); err != nil {
		t.Fatalf("promote bob: %v", err)
	}
	resp, err := rg.client.UpdateRoom(alice, &chatimv1.UpdateRoomRequest{RoomId: room, Name: new("Design")})
	if err != nil || !resp.GetChanged() || resp.GetRoom().GetName() != "Design" || resp.GetRoom().GetInfoVer() != 2 {
		t.Fatalf("owner UpdateRoom = %v, %v; want Design at info_ver 2", resp, err)
	}
	resp, err = rg.client.UpdateRoom(bob, &chatimv1.UpdateRoomRequest{RoomId: room, Avatar: new("files/av/1.png")})
	if err != nil || !resp.GetChanged() || resp.GetRoom().GetAvatar() != "files/av/1.png" || resp.GetRoom().GetName() != "Design" {
		t.Fatalf("admin UpdateRoom = %v, %v; want the avatar set and the name kept", resp, err)
	}
	resp, err = rg.client.UpdateRoom(bob, &chatimv1.UpdateRoomRequest{RoomId: room, Name: new("Design")})
	if err != nil || resp.GetChanged() || resp.GetRoom().GetInfoVer() != 3 {
		t.Fatalf("same name UpdateRoom = %v, %v; want unchanged at info_ver 3", resp, err)
	}
	_, err = rg.client.UpdateRoom(as(t, "acme", "carol"), &chatimv1.UpdateRoomRequest{RoomId: room, Name: new("Mine")})
	expectCode(t, err, codes.PermissionDenied)
	_, err = rg.client.UpdateRoom(alice, &chatimv1.UpdateRoomRequest{RoomId: room})
	expectCode(t, err, codes.InvalidArgument)
	dm, err := rg.client.OpenDirectRoom(alice, &chatimv1.OpenDirectRoomRequest{OtherUser: "bob"})
	if err != nil {
		t.Fatalf("OpenDirectRoom: %v", err)
	}
	_, err = rg.client.UpdateRoom(alice, &chatimv1.UpdateRoomRequest{RoomId: dm.GetRoom().GetId(), Name: new("Us")})
	expectCode(t, err, codes.FailedPrecondition)
	id := roomNumber(t, room)
	var got []string
	_, all := events.enqueued()
	for _, ev := range all {
		if ev.GetRoomUpdated() != nil {
			got = append(got, ev.GetId())
		}
	}
	if len(got) != 2 || got[0] != pbconv.RoomInfoEventID(id, 2) || got[1] != pbconv.RoomInfoEventID(id, 3) {
		t.Fatalf("room_updated ids %v, want v2 and v3", got)
	}
}

func TestSetRoomPreferencesThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	rg := newRig(t, options{events: events, now: func() time.Time { return now }})
	room := rg.createGroup(t, "acme", "alice", "bob")
	bob := as(t, "acme", "bob")
	until := now.Add(24 * time.Hour)
	resp, err := rg.client.SetRoomPreferences(bob, &chatimv1.SetRoomPreferencesRequest{RoomId: room, Pinned: new(true), MutedUntil: timestamppb.New(until)})
	state := resp.GetState()
	if err != nil || !resp.GetChanged() || state.GetPreferenceVer() != 1 || !state.GetPinnedAt().AsTime().Equal(now) ||
		!state.GetMutedUntil().AsTime().Equal(until) || state.GetArchivedAt() != nil || state.GetRole() != chatimv1.MemberRole_MEMBER_ROLE_MEMBER {
		t.Fatalf("SetRoomPreferences = %v, %v; want pinned and muted at preference_ver 1", resp, err)
	}
	resp, err = rg.client.SetRoomPreferences(bob, &chatimv1.SetRoomPreferencesRequest{RoomId: room, MutedUntil: timestamppb.New(now.Add(-time.Hour)), Pinned: new(true)})
	if err != nil || !resp.GetChanged() || resp.GetState().GetMutedUntil() != nil || resp.GetState().GetPinnedAt() == nil || resp.GetState().GetPreferenceVer() != 2 {
		t.Fatalf("unmute = %v, %v; want unmuted and still pinned at preference_ver 2", resp, err)
	}
	resp, err = rg.client.SetRoomPreferences(bob, &chatimv1.SetRoomPreferencesRequest{RoomId: room, Archived: new(false)})
	if err != nil || resp.GetChanged() || resp.GetState().GetPreferenceVer() != 2 {
		t.Fatalf("no-op = %v, %v; want unchanged at preference_ver 2", resp, err)
	}
	_, err = rg.client.SetRoomPreferences(as(t, "acme", "mallory"), &chatimv1.SetRoomPreferencesRequest{RoomId: room, Pinned: new(true)})
	expectCode(t, err, codes.PermissionDenied)
	_, err = rg.client.SetRoomPreferences(bob, &chatimv1.SetRoomPreferencesRequest{RoomId: room})
	expectCode(t, err, codes.InvalidArgument)
	id := roomNumber(t, room)
	var got []*chatimv1.Event
	_, all := events.enqueued()
	for _, ev := range all {
		if ev.GetRoomPreferencesChanged() != nil {
			got = append(got, ev)
		}
	}
	if len(got) != 2 || got[0].GetId() != pbconv.PreferencesEventID(id, "bob", 1) || got[1].GetId() != pbconv.PreferencesEventID(id, "bob", 2) ||
		got[1].GetActor() != "bob" || got[1].GetRoomPreferencesChanged().GetUser() != "bob" || !proto.Equal(got[1].GetRoomPreferencesChanged().GetState(), resp.GetState()) {
		t.Fatalf("room_preferences_changed = %v, want v1 then v2 carrying the final state", got)
	}
}
