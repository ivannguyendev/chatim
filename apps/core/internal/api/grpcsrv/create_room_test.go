package grpcsrv_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestCreateRoomStoresAndReturnsTheRoom(t *testing.T) {
	clock := time.Date(2026, 10, 2, 9, 30, 0, 123_456_789, time.FixedZone("ICT", 7*3600))
	newID, _ := idSequence(42)
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, now: func() time.Time { return clock }})
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"bob", "alice", "bob"}, RequestId: "r1",
	})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	stamp := clock.UTC().Truncate(time.Millisecond)
	want := &chatimv1.Room{
		Id: "42", Tenant: "acme", Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", CreatedBy: "alice",
		CreatedAt: timestamppb.New(stamp), MemberCount: 2,
	}
	if !proto.Equal(resp.GetRoom(), want) {
		t.Fatalf("room = %v, want %v", resp.GetRoom(), want)
	}
	stored, err := rg.rooms.Get(t.Context(), 42)
	if err != nil || stored.Tenant != "acme" || !stored.CreatedAt.Equal(stamp) || stored.MemberCount != 2 {
		t.Fatalf("stored room = %+v, %v", stored, err)
	}
	for user, role := range map[string]domain.Role{"alice": domain.RoleOwner, "bob": domain.RoleMember} {
		if m, err := rg.rooms.Member(t.Context(), 42, user); err != nil || m.Role != role {
			t.Errorf("member %s = %+v, %v; want role %s", user, m, err, role)
		}
	}
}

func TestCreateRoomRejectsBadInput(t *testing.T) {
	rg := newRig(t, options{sender: &fakeSender{}})
	dm, group := chatimv1.RoomType_ROOM_TYPE_DM, chatimv1.RoomType_ROOM_TYPE_GROUP
	cases := map[string]*chatimv1.CreateRoomRequest{
		"unspecified type":     {Name: "Team", Members: []string{"alice"}, RequestId: "r1"},
		"unknown type":         {Type: chatimv1.RoomType(7), Name: "Team", Members: []string{"alice"}, RequestId: "r1"},
		"group without name":   {Type: group, Members: []string{"alice"}, RequestId: "r1"},
		"name too long":        {Type: group, Name: strings.Repeat("n", 129), Members: []string{"alice"}, RequestId: "r1"},
		"creator not a member": {Type: group, Name: "Team", Members: []string{"bob"}, RequestId: "r1"},
		"no members":           {Type: group, Name: "Team", RequestId: "r1"},
		"invalid member id":    {Type: group, Name: "Team", Members: []string{"alice", "b.b"}, RequestId: "r1"},
		"no request id":        {Type: group, Name: "Team", Members: []string{"alice"}},
		"bad request id":       {Type: group, Name: "Team", Members: []string{"alice"}, RequestId: "r 1"},
		"direct room":          {Type: dm, Members: []string{"alice", "bob"}, RequestId: "r1"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.CreateRoom(as(t, "acme", "alice"), req)
			expectCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestCreateRoomCapsTheMembersOfOneRequest(t *testing.T) {
	newID, calls := idSequence(9)
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID})
	full := manyUsers("alice", 500)
	_, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Big", Members: append(full, "alice"), RequestId: "big-1",
	})
	expectCode(t, err, codes.InvalidArgument)
	if calls() != 0 {
		t.Fatalf("drew %d ids for a request over the cap, want 0", calls())
	}
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Big", Members: full, RequestId: "big-2",
	})
	if err != nil || resp.GetRoom().GetMemberCount() != 500 {
		t.Fatalf("CreateRoom of 500 = %v, %v; want 500 members", resp.GetRoom(), err)
	}
}

func TestCreateRoomRetryWithTheSameRequestGivesTheSameRoom(t *testing.T) {
	newID, _ := idSequence(42, 43)
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID})
	req := &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"alice", "bob"}, RequestId: "r1"}
	for i := range 2 {
		resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), req)
		if err != nil || resp.GetRoom().GetId() != "42" || resp.GetRoom().GetMemberCount() != 2 {
			t.Fatalf("CreateRoom call %d = %v, %v; want room 42 of 2", i+1, resp.GetRoom(), err)
		}
	}
	if _, err := rg.rooms.Get(t.Context(), 43); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("room 43 = %v, want ErrRoomNotFound", err)
	}
	other, err := rg.client.CreateRoom(as(t, "acme", "bob"), req)
	if err != nil || other.GetRoom().GetId() != "43" {
		t.Fatalf("CreateRoom by bob with alice's request id = %v, %v; want room 43", other.GetRoom(), err)
	}
}

func TestCreateRoomRefusesATakenIDWithoutDrawingAgain(t *testing.T) {
	newID, calls := idSequence(1, 2)
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID})
	occupy(t, rg.rooms, 1)
	req := &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"alice"}, RequestId: "r1"}
	_, err := rg.client.CreateRoom(as(t, "acme", "alice"), req)
	expectCode(t, err, codes.Unavailable)
	if calls() != 1 {
		t.Fatalf("drew %d ids, want 1", calls())
	}
	if taken, err := rg.rooms.Get(t.Context(), 1); err != nil || taken.Tenant != "other" {
		t.Fatalf("room 1 = %+v, %v; want the other tenant's room kept", taken, err)
	}
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), req)
	if err != nil || resp.GetRoom().GetId() != "2" {
		t.Fatalf("retry after a taken id = %v, %v; want room 2", resp.GetRoom(), err)
	}
}

func TestCreateRoomRefusesARequestInFlight(t *testing.T) {
	rg := newRig(t, options{sender: &fakeSender{}, pending: pendingCIDs{}})
	_, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"alice"}, RequestId: "r1",
	})
	expectCode(t, err, codes.Unavailable)
}

func occupy(t *testing.T, rooms *memstore.Rooms, ids ...uint64) {
	t.Helper()
	for _, id := range ids {
		room, members, err := domain.NewRoom("other", "owner", domain.RoomGroup, "Taken", []string{"owner"}, time.Now(), id)
		if err != nil {
			t.Fatalf("NewRoom(%d): %v", id, err)
		}
		if err := rooms.Create(t.Context(), room, members); err != nil {
			t.Fatalf("Create(%d): %v", id, err)
		}
	}
}
