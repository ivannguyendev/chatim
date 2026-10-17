package grpcsrv_test

import (
	"bytes"
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var leftMeta = []byte(`{"event":"member_left","user":"lan"}`)

func systemRequest(room, cid string) *chatimv1.SendMessageRequest {
	return &chatimv1.SendMessageRequest{RoomId: room, Cid: cid, Text: "Lan left", Kind: chatimv1.MessageKind_MESSAGE_KIND_SYSTEM, SkipUnread: true, Meta: leftMeta}
}

func TestSendMessageMapsKindSkipUnreadAndMeta(t *testing.T) {
	sender := &fakeSender{}
	rg := newRig(t, options{sender: sender})
	if _, err := rg.client.SendMessage(as(t, "acme", "lan"), systemRequest("42", "sys-1")); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	want := actor.SendCmd{Tenant: "acme", User: "lan", Room: 42, CID: "sys-1", Text: "Lan left", Kind: domain.KindSystem, SkipUnread: true, Meta: leftMeta}
	if cmds := sender.sent(); len(cmds) != 1 || !reflect.DeepEqual(cmds[0], want) {
		t.Fatalf("sender got %+v, want [%+v]", cmds, want)
	}
}

func TestSystemMessageFromANonMemberShowsInHistory(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	resp, err := rg.client.SendMessage(as(t, "acme", "sysmsg-bot"), systemRequest(room, "sys-1"))
	if err != nil || resp.GetSeq() != 1 {
		t.Fatalf("system send = %v, %v; want seq 1", resp, err)
	}
	text := &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-2", Text: "hi", Meta: []byte("render:card")}
	if _, err := rg.client.SendMessage(as(t, "acme", "bob"), text); err != nil {
		t.Fatalf("text send with meta: %v", err)
	}
	got := historyOf(t, rg, room)
	if len(got) != 2 {
		t.Fatalf("history = %v, want two messages", got)
	}
	sys, plain := got[0], got[1]
	if sys.GetKind() != chatimv1.MessageKind_MESSAGE_KIND_SYSTEM || !sys.GetSkipUnread() || !bytes.Equal(sys.GetMeta(), leftMeta) || sys.GetSender() != "sysmsg-bot" {
		t.Fatalf("system message = %v, want kind system with skip_unread and meta", sys)
	}
	if plain.GetKind() != chatimv1.MessageKind_MESSAGE_KIND_TEXT || plain.GetSkipUnread() || string(plain.GetMeta()) != "render:card" {
		t.Fatalf("text message = %v, want kind text with meta", plain)
	}
	_, err = rg.client.SendMessage(as(t, "acme", "sysmsg-bot"), &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-3", Text: "hi"})
	expectCode(t, err, codes.PermissionDenied)
	_, err = rg.client.SendMessage(as(t, "other", "sysmsg-bot"), systemRequest(room, "sys-4"))
	expectCode(t, err, codes.NotFound)
}

func TestSendMessageRejectsBadSystemMessagesBeforeAnyRead(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	withReply := systemRequest(room, "s-1")
	withReply.ReplyTo = &chatimv1.ReplyRef{Seq: 5}
	withForward := systemRequest(room, "s-2")
	withForward.ForwardFrom = &chatimv1.ForwardRef{RoomId: room, Seq: 5}
	withMention := systemRequest(room, "s-3")
	withMention.Mentions = &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "alice"}}}
	withAll := systemRequest(room, "s-4")
	withAll.Mentions = &chatimv1.MentionSet{All: true}
	bigMeta := &chatimv1.SendMessageRequest{RoomId: room, Cid: "s-5", Text: "hi", Meta: bytes.Repeat([]byte("a"), 8193)}
	badKind := &chatimv1.SendMessageRequest{RoomId: room, Cid: "s-6", Text: "hi", Kind: chatimv1.MessageKind(99)}
	for name, req := range map[string]*chatimv1.SendMessageRequest{
		"reply": withReply, "forward": withForward, "mention": withMention, "@all": withAll, "meta over 8KB": bigMeta, "unknown kind": badKind,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.SendMessage(as(t, "acme", "sysmsg-bot"), req)
			expectCode(t, err, codes.InvalidArgument)
		})
	}
	if got := historyOf(t, rg, room); len(got) != 0 {
		t.Fatalf("history = %v, want nothing written", got)
	}
}
