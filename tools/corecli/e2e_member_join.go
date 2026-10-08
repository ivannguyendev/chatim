package main

import (
	"fmt"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const historyPage = 100

func (r *memberRun) directRoom() error {
	user := r.st.User
	resp, _, err := r.cl.CreateRoom(r.as(user), &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{user, bob}})
	if err != nil {
		return fmt.Errorf("create direct room: %w", err)
	}
	dm := resp.GetRoom().GetId()
	r.dm = dm
	_, _, err = r.cl.AddMembers(r.as(user), &chatimv1.AddMembersRequest{RoomId: dm, Users: []string{carol}, RequestId: "e2e-dm-add"})
	if err = refused("add-members on the direct room", codes.FailedPrecondition, err); err != nil {
		return err
	}
	_, _, err = r.cl.RemoveMember(r.as(user), &chatimv1.RemoveMemberRequest{RoomId: dm, User: bob})
	if err = refused("remove-member on the direct room", codes.FailedPrecondition, err); err != nil {
		return err
	}
	_, _, err = r.cl.LeaveRoom(r.as(user), &chatimv1.LeaveRoomRequest{RoomId: dm})
	if err = refused("leave on the direct room", codes.FailedPrecondition, err); err != nil {
		return err
	}
	_, _, err = r.cl.ChangeMemberRole(r.as(user), &chatimv1.ChangeMemberRoleRequest{RoomId: dm, User: bob, Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN})
	if err = refused("set-role on the direct room", codes.FailedPrecondition, err); err != nil {
		return err
	}
	const cid = "e2e-dm-1"
	sent, _, err := r.cl.SendMessage(r.as(user), &chatimv1.SendMessageRequest{RoomId: dm, Cid: cid, Text: e2e.TextFor(cid)})
	if err == nil && sent.GetSeq() != 1 {
		err = fmt.Errorf("got seq %d, want 1", sent.GetSeq())
	}
	if err != nil {
		return fmt.Errorf("send to the direct room: %w", err)
	}
	if err := r.markRead(dm, bob, 1, 1); err != nil {
		return err
	}
	return r.expect(
		r.want(dm, e2e.KindRoomCreated, e2e.RoomCreatedEventID(dm), ""),
		r.member(dm, e2e.KindMemberAdded, user, 1, ""),
		r.member(dm, e2e.KindMemberAdded, bob, 1, ""),
		r.want(dm, e2e.KindCreated, e2e.MessageEventID(dm, 1), ""),
		r.want(dm, e2e.KindRead, e2e.ReadEventID(dm, bob, 1), e2e.ReadPayload(1, 1)),
	)
}

func (r *memberRun) addMembers(caller, requestID, want string, users ...string) error {
	for i := range 2 {
		resp, _, err := r.cl.AddMembers(r.as(caller), &chatimv1.AddMembersRequest{RoomId: r.st.Room, Users: users, RequestId: requestID})
		if err != nil {
			return fmt.Errorf("add-members %s (call %d): %w", requestID, i+1, err)
		}
		if got := e2e.FormatAdded(resp.GetAdded()); got != want {
			return fmt.Errorf("add-members %s (call %d) added [%s], want [%s]", requestID, i+1, got, want)
		}
	}
	return nil
}

func (r *memberRun) addTwo() error {
	if err := r.addMembers(r.st.User, "e2e-add-1", bob+":1,"+carol+":1", bob, carol); err != nil {
		return err
	}
	joined := e2e.AddedPayload("member", r.n, 1)
	return r.expect(
		r.member(r.st.Room, e2e.KindMemberAdded, bob, 1, joined),
		r.member(r.st.Room, e2e.KindMemberAdded, carol, 1, joined),
		r.count(2, r.st.Members+2),
	)
}

func (r *memberRun) joinedReadsAll() error {
	pages := len(r.st.Acks)/historyPage + 2
	all, err := walk(r.as(bob), r.cl, r.st.Room, historyPage, pages, true)
	if err == nil {
		err = e2e.CheckPage(r.st.Acks, r.st.Changes, all, r.st.Room, r.st.User)
	}
	if err != nil {
		return fmt.Errorf("history of %s: %w", bob, err)
	}
	return r.markRead(r.st.Room, bob, r.n, 1)
}

func (r *memberRun) markRead(room, user string, wantSeq, wantVer uint64) error {
	resp, _, err := r.cl.MarkRead(r.as(user), &chatimv1.MarkReadRequest{RoomId: room})
	if err != nil {
		return fmt.Errorf("read by %s: %w", user, err)
	}
	return e2e.CheckReadReply("read by "+user, wantSeq, wantVer, resp.GetReadSeq(), resp.GetReadVer())
}

func (r *memberRun) setRole(caller, target string, role chatimv1.MemberRole, first e2e.MemberReply) error {
	for i, want := range []e2e.MemberReply{first, {Ver: first.Ver}} {
		resp, _, err := r.cl.ChangeMemberRole(r.as(caller), &chatimv1.ChangeMemberRoleRequest{RoomId: r.st.Room, User: target, Role: role})
		if err != nil {
			return fmt.Errorf("set-role %s (call %d): %w", target, i+1, err)
		}
		got := e2e.MemberReply{Changed: resp.GetChanged(), Ver: resp.GetVer(), Detail: "previous_role=" + e2e.RoleName(resp.GetPreviousRole())}
		if err := e2e.CheckMemberReply(fmt.Sprintf("set-role %s (call %d)", target, i+1), want, got); err != nil {
			return err
		}
	}
	return nil
}

func (r *memberRun) promoteBob() error {
	if err := r.setRole(r.st.User, bob, chatimv1.MemberRole_MEMBER_ROLE_ADMIN, e2e.MemberReply{Changed: true, Ver: 2, Detail: "previous_role=member"}); err != nil {
		return err
	}
	first := e2e.MemberReply{Changed: true, Ver: 3, Detail: "previous_priority=0"}
	for i, want := range []e2e.MemberReply{first, {Ver: 3}} {
		resp, _, err := r.cl.SetMemberPriority(r.as(r.st.User), &chatimv1.SetMemberPriorityRequest{RoomId: r.st.Room, User: bob, Priority: 5})
		if err != nil {
			return fmt.Errorf("set-priority (call %d): %w", i+1, err)
		}
		got := e2e.MemberReply{Changed: resp.GetChanged(), Ver: resp.GetVer(), Detail: fmt.Sprintf("previous_priority=%d", resp.GetPreviousPriority())}
		if err := e2e.CheckMemberReply(fmt.Sprintf("set-priority (call %d)", i+1), want, got); err != nil {
			return err
		}
	}
	return r.expect(
		r.member(r.st.Room, e2e.KindRoleChanged, bob, 2, e2e.RolePayload("admin", "member")),
		r.member(r.st.Room, e2e.KindPriorityChanged, bob, 3, e2e.PriorityPayload(5, 0)),
	)
}
