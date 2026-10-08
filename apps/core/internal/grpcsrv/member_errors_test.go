package grpcsrv_test

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMemberErrorsKeepTheirCodes(t *testing.T) {
	rg := newRig(t, options{limits: mutate.Limits{MemberBatch: 3}})
	room := rg.createGroup(t, "acme", "alice", "bob", "carol")
	alice, bob, carol, mallory := as(t, "acme", "alice"), as(t, "acme", "bob"), as(t, "acme", "carol"), as(t, "acme", "mallory")
	if _, err := rg.client.ChangeMemberRole(alice, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: "carol", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN}); err != nil {
		t.Fatalf("ChangeMemberRole: %v", err)
	}
	dm, err := rg.client.CreateRoom(alice, &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{"alice", "bob"}})
	if err != nil {
		t.Fatalf("CreateRoom DM: %v", err)
	}
	add := func(ctx context.Context, room, requestID string, users ...string) func() error {
		return func() error {
			_, err := rg.client.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: room, Users: users, RequestId: requestID})
			return err
		}
	}
	remove := func(ctx context.Context, user string) func() error {
		return func() error {
			_, err := rg.client.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: room, User: user})
			return err
		}
	}
	setRole := func(ctx context.Context, user string, role chatimv1.MemberRole) func() error {
		return func() error {
			_, err := rg.client.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: user, Role: role})
			return err
		}
	}
	leave := func(ctx context.Context) func() error {
		return func() error {
			_, err := rg.client.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: room})
			return err
		}
	}
	cases := []struct {
		name string
		call func() error
		code codes.Code
	}{
		{"add to a direct room", add(alice, dm.GetRoom().GetId(), "r-1", "carol"), codes.FailedPrecondition},
		{"last owner steps down", setRole(alice, "alice", chatimv1.MemberRole_MEMBER_ROLE_MEMBER), codes.FailedPrecondition},
		{"stranger adds", add(mallory, room, "r-2", "dave"), codes.PermissionDenied},
		{"member adds", add(bob, room, "r-3", "dave"), codes.PermissionDenied},
		{"stranger leaves", leave(mallory), codes.PermissionDenied},
		{"member removes a stranger", remove(bob, "zed"), codes.PermissionDenied},
		{"admin removes a stranger", remove(carol, "zed"), codes.NotFound},
		{"role of a stranger", setRole(alice, "zed", chatimv1.MemberRole_MEMBER_ROLE_ADMIN), codes.NotFound},
		{"remove oneself", remove(bob, "bob"), codes.InvalidArgument},
		{"unspecified role", setRole(alice, "bob", chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED), codes.InvalidArgument},
		{"unknown role", setRole(alice, "bob", chatimv1.MemberRole(9)), codes.InvalidArgument},
		{"over the member batch", add(alice, room, "r-4", "dave", "erin", "fay", "gus"), codes.InvalidArgument},
		{"no users", add(alice, room, "r-5"), codes.InvalidArgument},
		{"no request id", add(alice, room, "", "dave"), codes.InvalidArgument},
		{"bad room id", add(alice, "x", "r-6", "dave"), codes.InvalidArgument},
		{"other tenant", add(as(t, "other", "alice"), room, "r-7", "dave"), codes.NotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { expectCode(t, c.call(), c.code) })
	}
	want := []string{"alice:owner:1", "bob:member:1", "carol:admin:2"}
	if got := rg.memberStates(t, room, "alice", "bob", "carol"); !slices.Equal(got, want) {
		t.Fatalf("members = %v, want %v untouched by the failed calls", got, want)
	}
	t.Run("lost write", expectALostWriteUnavailable)
}

func expectALostWriteUnavailable(t *testing.T) {
	var rg *rig
	var fired atomic.Bool
	rival := access.PolicyFunc(func(ctx context.Context, req access.Request) error {
		if req.Action == access.RemoveMember && fired.CompareAndSwap(false, true) {
			cur := req.Target
			next := cur.Next(cur.Role, domain.MemberActive, 5, "rival", "alice", time.Now())
			if ok, err := rg.rooms.ApplyMember(ctx, cur, next); err != nil || !ok {
				t.Errorf("rival write = %v, %v", ok, err)
			}
		}
		return access.DefaultPolicy{}.Check(ctx, req)
	})
	rg = newRig(t, options{policy: rival})
	room := rg.createGroup(t, "acme", "alice", "bob")
	_, err := rg.client.RemoveMember(as(t, "acme", "alice"), &chatimv1.RemoveMemberRequest{RoomId: room, User: "bob"})
	expectCode(t, err, codes.Unavailable)
	if got := rg.memberStates(t, room, "bob"); len(got) != 1 || got[0] != "bob:member:2" {
		t.Fatalf("bob = %v, want only the rival write", got)
	}
}
