package grpcsrv_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func membersSeen(t *testing.T, rg *rig, ctx context.Context, room, after string, limit uint32) ([]string, string) {
	t.Helper()
	resp, err := rg.client.ListMembers(ctx, &chatimv1.ListMembersRequest{RoomId: room, After: after, Limit: limit})
	if err != nil {
		t.Fatalf("ListMembers(after %q): %v", after, err)
	}
	var out []string
	for _, m := range resp.GetMembers() {
		out = append(out, fmt.Sprintf("%s %s p%d +%ds r%d/%d", m.GetUser(), m.GetRole(), m.GetPriority(),
			int(m.GetJoinedAt().AsTime().Sub(sentBase).Seconds()), m.GetReadSeq(), m.GetReadVer()))
	}
	return out, resp.GetNext()
}

func rankedRoom(t *testing.T) (*rig, string) {
	t.Helper()
	clock := &testClock{now: sentBase}
	rg := newRig(t, options{now: clock.Now})
	room := rg.createGroup(t, "acme", "alice", "bob", "carol")
	alice := as(t, "acme", "alice")
	clock.set(sentBase.Add(time.Second))
	if _, err := rg.client.AddMembers(alice, &chatimv1.AddMembersRequest{RoomId: room, Users: []string{"dan", "erin", "gus"}, RequestId: "r-1"}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	for _, user := range []string{"carol", "erin"} {
		if _, err := rg.client.ChangeMemberRole(alice, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: user, Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN}); err != nil {
			t.Fatalf("ChangeMemberRole(%s): %v", user, err)
		}
	}
	if _, err := rg.client.SetMemberPriority(alice, &chatimv1.SetMemberPriorityRequest{RoomId: room, User: "bob", Priority: 3}); err != nil {
		t.Fatalf("SetMemberPriority: %v", err)
	}
	if _, err := rg.client.RemoveMember(alice, &chatimv1.RemoveMemberRequest{RoomId: room, User: "gus"}); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	rg.insertAt(t, room, 1, "hi", sentAtSeq(1))
	if _, err := rg.client.MarkRead(as(t, "acme", "bob"), &chatimv1.MarkReadRequest{RoomId: room, Seq: 1}); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	return rg, room
}

func TestListMembersPagesOwnersThenAdminsThenMembers(t *testing.T) {
	rg, room := rankedRoom(t)
	bob := as(t, "acme", "bob")
	var got, cursors []string
	after := ""
	for range 3 {
		page, next := membersSeen(t, rg, bob, room, after, 2)
		got, after = append(got, page...), next
		cursors = append(cursors, next)
	}
	want := []string{
		"alice MEMBER_ROLE_OWNER p0 +0s r0/1", "carol MEMBER_ROLE_ADMIN p0 +0s r0/1",
		"erin MEMBER_ROLE_ADMIN p0 +1s r0/1", "bob MEMBER_ROLE_MEMBER p3 +0s r1/2",
		"dan MEMBER_ROLE_MEMBER p0 +1s r0/1",
	}
	if !slices.Equal(got, want) || cursors[0] == "" || cursors[1] == "" || cursors[2] != "" {
		t.Fatalf("members = %v cursors %q,\nwant %v and a cursor on the two full pages", got, cursors, want)
	}
	if all, next := membersSeen(t, rg, bob, room, "", 0); !slices.Equal(all, want) || next != "" {
		t.Fatalf("default page = %v next %q, want %v and no cursor", all, next, want)
	}
}

func TestListMembersOfADirectRoomListsBothPeople(t *testing.T) {
	rg := newRig(t, options{})
	dm, err := rg.client.OpenDirectRoom(as(t, "acme", "alice"), &chatimv1.OpenDirectRoomRequest{OtherUser: "bob"})
	if err != nil {
		t.Fatalf("OpenDirectRoom: %v", err)
	}
	got, _ := membersSeen(t, rg, as(t, "acme", "bob"), dm.GetRoom().GetId(), "", 0)
	if len(got) != 2 {
		t.Fatalf("DM members = %v, want both people", got)
	}
}

func TestListMembersCodes(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	cases := map[string]struct {
		user string
		req  *chatimv1.ListMembersRequest
		code codes.Code
	}{
		"non-member":    {"carol", &chatimv1.ListMembersRequest{RoomId: room}, codes.PermissionDenied},
		"bad cursor":    {"bob", &chatimv1.ListMembersRequest{RoomId: room, After: "x"}, codes.InvalidArgument},
		"limit too big": {"bob", &chatimv1.ListMembersRequest{RoomId: room, Limit: 1001}, codes.InvalidArgument},
	}
	for name, c := range cases {
		_, err := rg.client.ListMembers(as(t, "acme", c.user), c.req)
		t.Run(name, func(t *testing.T) { expectCode(t, err, c.code) })
	}
	if got, _ := membersSeen(t, rg, as(t, "acme", "bob"), room, "", 1000); len(got) != 2 {
		t.Fatalf("limit 1000 = %v, want both members", got)
	}
}
