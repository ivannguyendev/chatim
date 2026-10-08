package main

import (
	"fmt"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func (r *memberRun) bobRemovesCarol() error {
	_, _, err := r.cl.RemoveMember(r.as(bob), &chatimv1.RemoveMemberRequest{RoomId: r.st.Room, User: r.st.User})
	if err := refused(bob+" removes the owner", codes.PermissionDenied, err); err != nil {
		return err
	}
	for i, want := range []e2e.MemberReply{{Changed: true, Ver: 2}, {Ver: 2}} {
		resp, _, err := r.cl.RemoveMember(r.as(bob), &chatimv1.RemoveMemberRequest{RoomId: r.st.Room, User: carol})
		if err != nil {
			return fmt.Errorf("remove-member %s (call %d): %w", carol, i+1, err)
		}
		got := e2e.MemberReply{Changed: resp.GetChanged(), Ver: resp.GetVer()}
		if err := e2e.CheckMemberReply(fmt.Sprintf("remove-member %s (call %d)", carol, i+1), want, got); err != nil {
			return err
		}
	}
	return r.expect(
		r.member(r.st.Room, e2e.KindMemberRemoved, carol, 2, e2e.RemovedPayload("removed", "member")),
		r.count(3, r.st.Members+1),
	)
}

func (r *memberRun) readsHistory(user string) error {
	_, _, err := r.cl.GetHistory(r.as(user), &chatimv1.GetHistoryRequest{RoomId: r.st.Room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 1})
	return err
}

func (r *memberRun) carolIsOut() error {
	const cid = "e2e-carol-1"
	_, _, err := r.cl.SendMessage(r.as(carol), &chatimv1.SendMessageRequest{RoomId: r.st.Room, Cid: cid, Text: e2e.TextFor(cid)})
	if err := refused(carol+" sends", codes.PermissionDenied, err); err != nil {
		return err
	}
	if err := refused(carol+" reads history", codes.PermissionDenied, r.readsHistory(carol)); err != nil {
		return err
	}
	_, _, err = r.cl.MarkRead(r.as(carol), &chatimv1.MarkReadRequest{RoomId: r.st.Room})
	return refused(carol+" marks read", codes.PermissionDenied, err)
}

func (r *memberRun) addAgainIsDone() error {
	resp, _, err := r.cl.AddMembers(r.as(r.st.User), &chatimv1.AddMembersRequest{RoomId: r.st.Room, Users: []string{bob, carol}, RequestId: "e2e-add-1"})
	if err != nil {
		return fmt.Errorf("add-members e2e-add-1 again: %w", err)
	}
	if got := e2e.FormatAdded(resp.GetAdded()); got != "" {
		return fmt.Errorf("add-members e2e-add-1 again added [%s], want []", got)
	}
	return refused(carol+" reads history after the resent add", codes.PermissionDenied, r.readsHistory(carol))
}

func (r *memberRun) unreadThenRead() error {
	resp, _, err := r.cl.MarkUnread(r.as(bob), &chatimv1.MarkUnreadRequest{RoomId: r.st.Room, Seq: r.n})
	if err != nil {
		return fmt.Errorf("unread by %s: %w", bob, err)
	}
	if err := e2e.CheckReadReply("unread by "+bob, r.n-1, 2, resp.GetReadSeq(), resp.GetReadVer()); err != nil {
		return err
	}
	if err := r.markRead(r.st.Room, bob, r.n, 3); err != nil {
		return err
	}
	return r.expect(
		r.want(r.st.Room, e2e.KindRead, e2e.ReadEventID(r.st.Room, bob, 2), e2e.ReadPayload(r.n-1, 2)),
		r.want(r.st.Room, e2e.KindRead, e2e.ReadEventID(r.st.Room, bob, 3), e2e.ReadPayload(r.n, 3)),
	)
}
