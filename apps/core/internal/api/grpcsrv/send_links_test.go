package grpcsrv_test

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func replyReq(room, cid string, seq uint64) *chatimv1.SendMessageRequest {
	return &chatimv1.SendMessageRequest{RoomId: room, Cid: cid, Text: "re", ReplyTo: &chatimv1.ReplyRef{Seq: seq}}
}

func historyOf(t *testing.T, rg *rig, room string) []*chatimv1.Message {
	t.Helper()
	resp, err := rg.client.GetHistory(as(t, "acme", "alice"), &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST})
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	return resp.GetMessages()
}

func TestSendMessageStoresTheReplyAndMentions(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "alice"), room, "c-1", "parent")
	req := replyReq(room, "c-2", 1)
	req.Mentions = &chatimv1.MentionSet{All: true, Targets: []*chatimv1.MentionTarget{
		{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "alice"},
		{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "alice"},
		{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "team-design"},
	}}
	if _, err := rg.client.SendMessage(as(t, "acme", "bob"), req); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	got := historyOf(t, rg, room)[1]
	want := []*chatimv1.MentionTarget{req.Mentions.Targets[0], req.Mentions.Targets[2]}
	if !proto.Equal(got.GetReplyTo(), &chatimv1.ReplyRef{Seq: 1}) || !got.GetMentionAll() || len(got.GetMentionTargets()) != 2 ||
		!proto.Equal(got.GetMentionTargets()[0], want[0]) || !proto.Equal(got.GetMentionTargets()[1], want[1]) {
		t.Fatalf("stored reply = %v, want reply_to seq 1, @all and mentions %v", got, want)
	}
}

func TestSendMessageRepliesToADeletedParent(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "alice"), room, "c-1", "parent")
	if _, err := rg.client.DeleteMessage(as(t, "acme", "alice"), &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	resp, err := rg.client.SendMessage(as(t, "acme", "bob"), replyReq(room, "c-2", 1))
	if err != nil || resp.GetSeq() != 2 {
		t.Fatalf("reply to a deleted parent = %v, %v; want seq 2", resp, err)
	}
}

func TestSendMessageRejectsRepliesToMissingParents(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	other := rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "alice"), other, "c-1", "elsewhere")
	rg.send(t, as(t, "acme", "alice"), other, "c-2", "elsewhere")
	rg.send(t, as(t, "acme", "alice"), room, "c-1", "here")
	for name, seq := range map[string]uint64{"missing seq": 9, "seq only in another room": 2} {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.SendMessage(as(t, "acme", "bob"), replyReq(room, "c-9", seq))
			expectCode(t, err, codes.NotFound)
		})
	}
	if n := len(historyOf(t, rg, room)); n != 1 {
		t.Fatalf("room has %d messages, want 1", n)
	}
}

func TestSendMessageNeverTellsOutsidersWhetherTheParentExists(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "alice"), room, "c-1", "parent")
	for name, seq := range map[string]uint64{"existing parent": 1, "missing parent": 9} {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.SendMessage(as(t, "acme", "mallory"), replyReq(room, "c-2", seq))
			expectCode(t, err, codes.PermissionDenied)
			_, err = rg.client.SendMessage(as(t, "other", "alice"), replyReq(room, "c-2", seq))
			expectCode(t, err, codes.NotFound)
		})
	}
}

func TestSendMessageRejectsBadLinks(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "alice"), room, "c-1", "parent")
	many := make([]*chatimv1.MentionTarget, 51)
	for i := range many {
		many[i] = &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: fmt.Sprintf("u%d", i)}
	}
	cases := map[string]*chatimv1.SendMessageRequest{
		"reply in a thread": {RoomId: room, Cid: "c-2", Text: "re", ReplyTo: &chatimv1.ReplyRef{ThreadRoot: 1, Seq: 1}},
		"reply to seq 0":    {RoomId: room, Cid: "c-2", Text: "re", ReplyTo: &chatimv1.ReplyRef{}},
		"51 mentions":       {RoomId: room, Cid: "c-2", Text: "hi", Mentions: &chatimv1.MentionSet{Targets: many}},
		"unknown mention":   {RoomId: room, Cid: "c-2", Text: "hi", Mentions: &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{{Id: "x"}}}},
		"forward and reply": {RoomId: room, Cid: "c-2", Text: "fw", ForwardFrom: &chatimv1.ForwardRef{RoomId: room, Seq: 1}, ReplyTo: &chatimv1.ReplyRef{Seq: 1}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.SendMessage(as(t, "acme", "bob"), req)
			expectCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestSendMessageAsksThePolicyForMentionAll(t *testing.T) {
	noAll := access.PolicyFunc(func(_ context.Context, r access.Request) error {
		if r.Action == access.MentionAll {
			return access.ErrDenied
		}
		return nil
	})
	rg := newRig(t, options{sendPol: noAll})
	room := rg.createGroup(t, "acme", "alice", "bob")
	_, err := rg.client.SendMessage(as(t, "acme", "bob"), &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-1", Text: "hi", Mentions: &chatimv1.MentionSet{All: true}})
	expectCode(t, err, codes.PermissionDenied)
	if resp := rg.send(t, as(t, "acme", "bob"), room, "c-2", "hi"); resp.GetSeq() != 1 {
		t.Fatalf("plain send = %v, want seq 1", resp)
	}
}

func TestSendMessageReadsNothingWithoutAReply(t *testing.T) {
	sender := &fakeSender{}
	rg := newRig(t, options{sender: sender})
	if _, err := rg.client.SendMessage(as(t, "acme", "alice"), &chatimv1.SendMessageRequest{RoomId: "42", Cid: "c-1", Text: "hi"}); err != nil {
		t.Fatalf("send to an unread room = %v, want the sender's ack", err)
	}
	_, err := rg.client.SendMessage(as(t, "acme", "alice"), replyReq("42", "c-2", 1))
	expectCode(t, err, codes.NotFound)
	if n := len(sender.sent()); n != 1 {
		t.Fatalf("sender got %d commands, want only the plain one", n)
	}
}
