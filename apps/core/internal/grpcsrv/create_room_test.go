package grpcsrv_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestCreateRoomStoresAndReturnsTheRoom(t *testing.T) {
	clock := time.Date(2026, 10, 2, 9, 30, 0, 123_456_789, time.FixedZone("ICT", 7*3600))
	newID, _ := idSequence(42)
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, now: func() time.Time { return clock }})
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"bob", "alice", "bob"},
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
		"unspecified type":       {Name: "Team", Members: []string{"alice"}},
		"unknown type":           {Type: chatimv1.RoomType(7), Name: "Team", Members: []string{"alice"}},
		"group without name":     {Type: group, Members: []string{"alice"}},
		"name too long":          {Type: group, Name: strings.Repeat("n", 129), Members: []string{"alice"}},
		"creator not a member":   {Type: group, Name: "Team", Members: []string{"bob"}},
		"no members":             {Type: group, Name: "Team"},
		"invalid member id":      {Type: group, Name: "Team", Members: []string{"alice", "b.b"}},
		"dm of one":              {Type: dm, Members: []string{"alice"}},
		"dm of three":            {Type: dm, Members: []string{"alice", "bob", "carol"}},
		"dm without the creator": {Type: dm, Members: []string{"bob", "carol"}},
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
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Big", Members: append(full, "alice"),
	})
	expectCode(t, err, codes.InvalidArgument)
	if calls() != 0 {
		t.Fatalf("drew %d ids for a request over the cap, want 0", calls())
	}
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Big", Members: full,
	})
	if err != nil || resp.GetRoom().GetMemberCount() != 500 {
		t.Fatalf("CreateRoom of 500 = %v, %v; want 500 members", resp.GetRoom(), err)
	}
}

func TestCreateRoomRetriesWithFreshIDsWhileTaken(t *testing.T) {
	newID, calls := idSequence(1, 2, 3)
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID})
	occupy(t, rg.rooms, 1, 2)
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{"alice", "bob"},
	})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	if resp.GetRoom().GetId() != "3" || calls() != 3 {
		t.Fatalf("room id %q after %d ids, want \"3\" after 3", resp.GetRoom().GetId(), calls())
	}
	if m, err := rg.rooms.Member(t.Context(), 3, "bob"); err != nil || m.Tenant != "acme" {
		t.Fatalf("member bob of room 3 = %+v, %v", m, err)
	}
}

func TestCreateRoomGivesUpAfterThreeTakenIDs(t *testing.T) {
	newID, calls := idSequence(1, 2, 3, 4)
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID})
	occupy(t, rg.rooms, 1, 2, 3)
	_, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"alice"},
	})
	expectCode(t, err, codes.Unavailable)
	if calls() != 3 {
		t.Fatalf("drew %d ids, want 3", calls())
	}
	if _, err := rg.rooms.Get(t.Context(), 4); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("room 4 = %v, want ErrRoomNotFound", err)
	}
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
