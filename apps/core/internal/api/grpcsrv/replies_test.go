package grpcsrv_test

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func sentAtSeq(seq uint64) time.Time { return sentBase.Add(time.Duration(seq) * time.Second) }

func (rg *rig) insertReply(t *testing.T, room string, seq, parent uint64) {
	t.Helper()
	id := roomNumber(t, room)
	m := domain.Message{
		Room: id, Seq: seq, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "r" + strconv.FormatUint(seq, 10),
		CID: "c-" + strconv.FormatUint(seq, 10), CreatedAt: sentAtSeq(seq), ReplyTo: &domain.ReplyRef{Seq: parent},
	}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert reply %d: %+v", seq, res)
	}
	r := domain.Reply{Parent: domain.MsgKey{Room: id, Seq: parent}, Room: id, Seq: seq, Tenant: "acme", From: "alice", At: sentAtSeq(seq)}
	if _, err := rg.reactions.AddReply(t.Context(), r); err != nil {
		t.Fatalf("AddReply %d: %v", seq, err)
	}
}

func repliesSeen(t *testing.T, rg *rig, ctx context.Context, room string, parent, after uint64, limit uint32) ([]shown, uint64) {
	t.Helper()
	resp, err := rg.client.GetReplies(ctx, &chatimv1.GetRepliesRequest{RoomId: room, Seq: parent, After: after, Limit: limit})
	if err != nil {
		t.Fatalf("GetReplies(%d after %d): %v", parent, after, err)
	}
	var out []shown
	for _, m := range resp.GetMessages() {
		out = append(out, shown{m.GetSeq(), m.GetText(), m.GetDeleted(), m.GetHidden(), m.GetVer()})
	}
	return out, resp.GetNext()
}

func TestGetRepliesPagesAndMasksPerViewer(t *testing.T) {
	clock := &testClock{now: sentBase}
	rg := newRig(t, options{now: clock.Now})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	rg.insertAt(t, room, 1, "parent", sentAtSeq(1))
	for seq := uint64(2); seq <= 6; seq++ {
		rg.insertReply(t, room, seq, 1)
	}
	rg.insertAt(t, room, 7, "other", sentAtSeq(7))
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 3}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 4}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	clock.set(sentAtSeq(2).Add(500 * time.Millisecond))
	if _, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room}); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	var got []shown
	var after uint64
	var cursors []uint64
	for range 3 {
		page, next := repliesSeen(t, rg, bob, room, 1, after, 2)
		got, after = append(got, page...), next
		cursors = append(cursors, next)
	}
	want := []shown{{2, "", false, true, 0}, {3, "", true, false, 1}, {4, "", false, true, 0}, {5, "r5", false, false, 0}, {6, "r6", false, false, 0}}
	if !slices.Equal(got, want) || !slices.Equal(cursors, []uint64{3, 5, 0}) {
		t.Fatalf("bob replies = %+v cursors %v, want %+v cursors [3 5 0]", got, cursors, want)
	}
	all, next := repliesSeen(t, rg, alice, room, 1, 0, 0)
	want = []shown{{2, "r2", false, false, 0}, {3, "", true, false, 1}, {4, "r4", false, false, 0}, {5, "r5", false, false, 0}, {6, "r6", false, false, 0}}
	if !slices.Equal(all, want) || next != 0 {
		t.Fatalf("alice replies = %+v next %d, want %+v and no cursor", all, next, want)
	}
	_, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1})
	expectCode(t, err, codes.FailedPrecondition)
}

func TestGetRepliesOfADeletedParentAndBadRequests(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice := as(t, "acme", "alice")
	rg.insertAt(t, room, 1, "parent", sentAtSeq(1))
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	rg.insertReply(t, room, 2, 1)
	if got, next := repliesSeen(t, rg, alice, room, 1, 0, 0); !slices.Equal(got, []shown{{2, "r2", false, false, 0}}) || next != 0 {
		t.Fatalf("replies of a deleted parent = %+v next %d, want [r2]", got, next)
	}
	if got, _ := repliesSeen(t, rg, alice, room, 2, 0, 0); len(got) != 0 {
		t.Fatalf("replies of a message without replies = %+v, want none", got)
	}
	for name, c := range map[string]struct {
		ctx  context.Context
		req  *chatimv1.GetRepliesRequest
		code codes.Code
	}{
		"missing parent":           {alice, &chatimv1.GetRepliesRequest{RoomId: room, Seq: 99}, codes.NotFound},
		"stranger, missing parent": {as(t, "acme", "carol"), &chatimv1.GetRepliesRequest{RoomId: room, Seq: 99}, codes.PermissionDenied},
		"stranger":                 {as(t, "acme", "carol"), &chatimv1.GetRepliesRequest{RoomId: room, Seq: 1}, codes.PermissionDenied},
		"no seq":                   {alice, &chatimv1.GetRepliesRequest{RoomId: room}, codes.InvalidArgument},
		"limit too large":          {alice, &chatimv1.GetRepliesRequest{RoomId: room, Seq: 1, Limit: 101}, codes.InvalidArgument},
		"bad room":                 {alice, &chatimv1.GetRepliesRequest{RoomId: "x", Seq: 1}, codes.InvalidArgument},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.GetReplies(c.ctx, c.req)
			expectCode(t, err, c.code)
		})
	}
}
