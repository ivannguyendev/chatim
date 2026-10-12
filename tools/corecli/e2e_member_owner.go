package main

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const memberCID = "e2e-member-1"

func (r *memberRun) hideAndClear() error {
	room := r.st.Room
	if _, _, err := r.cl.HideMessage(r.as(bob), &chatimv1.HideMessageRequest{RoomId: room, Seq: 1}); err != nil {
		return fmt.Errorf("hide seq 1 by %s: %w", bob, err)
	}
	cleared, _, err := r.cl.ClearHistory(r.as(bob), &chatimv1.ClearHistoryRequest{RoomId: room})
	if err == nil && cleared.GetClearedAt() == nil {
		err = errors.New("no cleared_at")
	}
	if err != nil {
		return fmt.Errorf("clear by %s: %w", bob, err)
	}
	at := cleared.GetClearedAt().AsTime()
	sent, _, err := r.cl.SendMessage(r.as(r.st.User), &chatimv1.SendMessageRequest{RoomId: room, Cid: memberCID, Text: e2e.TextFor(memberCID)})
	if err == nil && sent.GetSeq() != r.n+1 {
		err = fmt.Errorf("got seq %d, want %d", sent.GetSeq(), r.n+1)
	}
	if err != nil {
		return fmt.Errorf("send after the clear: %w", err)
	}
	ack := e2e.Ack{CID: memberCID, Seq: sent.GetSeq()}
	r.st.Acks = append(r.st.Acks, ack)
	latest, err := page(r.as(bob), r.cl, room, chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, 0, 2)
	if err == nil {
		err = e2e.CheckClearedPage(latest, r.n, ack)
	}
	if err != nil {
		return fmt.Errorf("history of %s after the clear: %w", bob, err)
	}
	return r.expect(
		r.want(room, e2e.KindHidden, e2e.HiddenEventID(room, bob, 0, 1), e2e.HiddenPayload(0, 1)),
		r.want(room, e2e.KindCleared, e2e.ClearedEventID(room, bob, at), e2e.ClearedPayload(at)),
		r.want(room, e2e.KindCreated, e2e.MessageEventID(room, ack.Seq), ""),
	)
}

func (r *memberRun) ownerLeaves() error {
	user := r.st.User
	for i, want := range []e2e.MemberReply{{Changed: true, Ver: 2, Detail: "new_owner=" + bob}, {Ver: 2}} {
		resp, _, err := r.cl.LeaveRoom(r.as(user), &chatimv1.LeaveRoomRequest{RoomId: r.st.Room})
		if err != nil {
			return fmt.Errorf("leave by %s (call %d): %w", user, i+1, err)
		}
		got := e2e.MemberReply{Changed: resp.GetChanged(), Ver: resp.GetVer(), Detail: "new_owner=" + resp.GetNewOwner()}
		if err := e2e.CheckMemberReply(fmt.Sprintf("leave by %s (call %d)", user, i+1), want, got); err != nil {
			return err
		}
	}
	if err := refused(user+" reads history after leaving", codes.PermissionDenied, r.readsHistory(user)); err != nil {
		return err
	}
	return r.expect(
		r.member(r.st.Room, e2e.KindRoleChanged, bob, 4, e2e.RolePayload("owner", "admin")),
		r.member(r.st.Room, e2e.KindMemberRemoved, user, 2, e2e.RemovedPayload("left", "owner")),
		r.count(4, r.st.Members),
	)
}

func (r *memberRun) ownerReturns() error {
	user := r.st.User
	if err := r.addMembers(bob, "e2e-add-3", user+":3", user); err != nil {
		return err
	}
	if err := r.setRole(bob, user, chatimv1.MemberRole_MEMBER_ROLE_OWNER, e2e.MemberReply{Changed: true, Ver: 4, Detail: "previous_role=member"}); err != nil {
		return err
	}
	last := r.n + 1
	if err := r.markRead(r.st.Room, user, last, 2); err != nil {
		return err
	}
	return r.expect(
		r.member(r.st.Room, e2e.KindMemberAdded, user, 3, e2e.AddedPayload("member", last, 2)),
		r.member(r.st.Room, e2e.KindRoleChanged, user, 4, e2e.RolePayload("owner", "member")),
		r.count(5, r.st.Members+1),
	)
}
