package grpcsrv_test

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMemberChangesThroughTheService(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, carol := as(t, "acme", "alice"), as(t, "acme", "carol")
	added := rg.add(t, alice, room, "r-1", "carol", "dave")
	if got := addedRows(added); !slices.Equal(got, []string{"carol:1", "dave:1"}) {
		t.Fatalf("AddMembers added %v, want carol and dave at ver 1", got)
	}
	role, err := rg.client.ChangeMemberRole(alice, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: "carol", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN})
	if err != nil || !role.GetChanged() || role.GetVer() != 2 || role.GetPreviousRole() != chatimv1.MemberRole_MEMBER_ROLE_MEMBER {
		t.Fatalf("ChangeMemberRole = %v, %v; want changed at ver 2 from member", role, err)
	}
	prio, err := rg.client.SetMemberPriority(alice, &chatimv1.SetMemberPriorityRequest{RoomId: room, User: "bob", Priority: 7})
	if err != nil || !prio.GetChanged() || prio.GetVer() != 2 || prio.GetPreviousPriority() != 0 {
		t.Fatalf("SetMemberPriority = %v, %v; want changed at ver 2 from 0", prio, err)
	}
	same, err := rg.client.SetMemberPriority(alice, &chatimv1.SetMemberPriorityRequest{RoomId: room, User: "bob", Priority: 7})
	if err != nil || same.GetChanged() || same.GetVer() != 2 || same.GetPreviousPriority() != 7 {
		t.Fatalf("repeated SetMemberPriority = %v, %v; want unchanged at ver 2", same, err)
	}
	removed, err := rg.client.RemoveMember(carol, &chatimv1.RemoveMemberRequest{RoomId: room, User: "dave"})
	if err != nil || !removed.GetChanged() || removed.GetVer() != 2 {
		t.Fatalf("admin RemoveMember = %v, %v; want changed at ver 2", removed, err)
	}
	left, err := rg.client.LeaveRoom(alice, &chatimv1.LeaveRoomRequest{RoomId: room})
	if err != nil || !left.GetChanged() || left.GetVer() != 2 || left.GetNewOwner() != "carol" {
		t.Fatalf("owner LeaveRoom = %v, %v; want changed at ver 2 with carol as the new owner", left, err)
	}
	again, err := rg.client.LeaveRoom(alice, &chatimv1.LeaveRoomRequest{RoomId: room})
	if err != nil || again.GetChanged() || again.GetNewOwner() != "" {
		t.Fatalf("second LeaveRoom = %v, %v; want unchanged", again, err)
	}
	want := []string{"alice:removed:2", "bob:member:2", "carol:owner:3", "dave:removed:2"}
	if got := rg.memberStates(t, room, "alice", "bob", "carol", "dave"); !slices.Equal(got, want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
}

func TestARemovedMemberCanNeitherSendNorReadUntilAddedBack(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	rg.send(t, bob, room, "c-1", "hi")
	if _, err := rg.client.RemoveMember(alice, &chatimv1.RemoveMemberRequest{RoomId: room, User: "bob"}); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	_, err := rg.client.SendMessage(bob, &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-2", Text: "still here?"})
	expectCode(t, err, codes.PermissionDenied)
	_, err = rg.client.GetHistory(bob, &chatimv1.GetHistoryRequest{RoomId: room})
	expectCode(t, err, codes.PermissionDenied)
	_, err = rg.client.MarkRead(bob, &chatimv1.MarkReadRequest{RoomId: room})
	expectCode(t, err, codes.PermissionDenied)
	if got := addedRows(rg.add(t, alice, room, "r-back", "bob")); !slices.Equal(got, []string{"bob:3"}) {
		t.Fatalf("AddMembers added %v, want bob back at ver 3", got)
	}
	if sent := rg.send(t, bob, room, "c-3", "back"); sent.GetSeq() != 2 {
		t.Fatalf("send after the re-add got seq %d, want 2", sent.GetSeq())
	}
	page, err := rg.client.GetHistory(bob, &chatimv1.GetHistoryRequest{RoomId: room})
	if err != nil || len(page.GetMessages()) != 2 {
		t.Fatalf("GetHistory after the re-add = %v, %v; want both messages", page, err)
	}
}

func TestARetriedAddNeverBringsBackSomeoneRemovedSince(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice := as(t, "acme", "alice")
	if got := addedRows(rg.add(t, alice, room, "r-1", "carol")); !slices.Equal(got, []string{"carol:1"}) {
		t.Fatalf("first AddMembers added %v, want carol", got)
	}
	if _, err := rg.client.RemoveMember(alice, &chatimv1.RemoveMemberRequest{RoomId: room, User: "carol"}); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if got := rg.add(t, alice, room, "r-1", "carol"); len(got) != 0 {
		t.Fatalf("retried AddMembers added %v, want nothing", addedRows(got))
	}
	if got := rg.memberStates(t, room, "carol"); !slices.Equal(got, []string{"carol:removed:2"}) {
		t.Fatalf("carol = %v, want still removed at ver 2", got)
	}
	_, err := rg.client.GetHistory(as(t, "acme", "carol"), &chatimv1.GetHistoryRequest{RoomId: room})
	expectCode(t, err, codes.PermissionDenied)
}

func (rg *rig) add(t *testing.T, ctx context.Context, room, requestID string, users ...string) []*chatimv1.AddedMember {
	t.Helper()
	resp, err := rg.client.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: room, Users: users, RequestId: requestID})
	if err != nil {
		t.Fatalf("AddMembers(%s): %v", requestID, err)
	}
	return resp.GetAdded()
}

func addedRows(added []*chatimv1.AddedMember) []string {
	out := make([]string, len(added))
	for i, a := range added {
		out[i] = a.GetUser() + ":" + strconv.FormatUint(uint64(a.GetVer()), 10)
	}
	return out
}

func (rg *rig) memberStates(t *testing.T, room string, users ...string) []string {
	t.Helper()
	docs, err := rg.rooms.MembersOf(t.Context(), roomNumber(t, room), users)
	if err != nil {
		t.Fatalf("MembersOf: %v", err)
	}
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		state := string(d.Role)
		if !d.Active() {
			state = "removed"
		}
		out = append(out, d.User+":"+state+":"+strconv.FormatUint(uint64(d.Ver), 10))
	}
	slices.Sort(out)
	return out
}
