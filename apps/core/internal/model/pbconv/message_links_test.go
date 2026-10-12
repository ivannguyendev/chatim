package pbconv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func linked() domain.Message {
	m := sample()
	m.ReplyTo = &domain.ReplyRef{Seq: 40}
	m.Forward = &domain.ForwardRef{Room: 555, Seq: 9, Author: "lan", SentAt: sentAt}
	m.Mentions = []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh"}, {Kind: domain.MentionGroup, ID: "team-design"}}
	m.MentionAll = true
	m.Replies = domain.ReplyCount{N: 2, Version: 3}
	return m
}

func wantLinks() *chatimv1.Message {
	return &chatimv1.Message{
		ReplyTo:     &chatimv1.ReplyRef{Seq: 40},
		ForwardFrom: &chatimv1.ForwardRef{RoomId: "555", Seq: 9, Author: "lan", SentAt: timestamppb.New(sentAt)},
		MentionTargets: []*chatimv1.MentionTarget{
			{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "minh"},
			{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "team-design"},
		},
		MentionAll: true,
		ReplyCount: &chatimv1.ReplyCount{Count: 2, Ver: 3},
	}
}

func linksOf(m *chatimv1.Message) *chatimv1.Message {
	return &chatimv1.Message{
		ReplyTo: m.GetReplyTo(), ForwardFrom: m.GetForwardFrom(), MentionTargets: m.GetMentionTargets(),
		MentionAll: m.GetMentionAll(), ReplyCount: m.GetReplyCount(),
	}
}

func TestMessageCarriesRepliesForwardsAndMentions(t *testing.T) {
	if got := linksOf(pbconv.Message(linked())); !proto.Equal(got, wantLinks()) {
		t.Fatalf("links = %v, want %v", got, wantLinks())
	}
	plain := pbconv.Message(sample())
	if plain.ReplyTo != nil || plain.ForwardFrom != nil || plain.MentionTargets != nil || plain.MentionAll || plain.ReplyCount != nil {
		t.Fatalf("plain message carries links: %v", plain)
	}
}

func TestMessageCreatedCarriesTheLinks(t *testing.T) {
	ev := pbconv.MessageCreated(domain.RoomGroup, linked())
	if got := linksOf(ev.GetMessageCreated().GetMessage()); !proto.Equal(got, wantLinks()) {
		t.Fatalf("msg_created links = %v, want %v", got, wantLinks())
	}
}

func TestDomainLinksFromRequests(t *testing.T) {
	if got := pbconv.DomainReplyRef(&chatimv1.ReplyRef{ThreadRoot: 3, Seq: 40}); *got != (domain.ReplyRef{Thread: 3, Seq: 40}) {
		t.Fatalf("DomainReplyRef = %+v", got)
	}
	if pbconv.DomainReplyRef(nil) != nil || pbconv.DomainMentionTargets(nil) != nil {
		t.Fatal("absent links must stay nil")
	}
	got := pbconv.DomainMentionTargets([]*chatimv1.MentionTarget{
		{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "minh"},
		{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "g"},
		{Kind: chatimv1.MentionKind_MENTION_KIND_UNSPECIFIED, Id: "x"},
	})
	want := []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh"}, {Kind: domain.MentionGroup, ID: "g"}, {Kind: 0, ID: "x"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("DomainMentionTargets = %+v, want %+v", got, want)
	}
}

func TestEventsOfADeletedMessageCarryNoContentLinks(t *testing.T) {
	m := linked()
	m.Deleted, m.Text = true, "leftover"
	gone := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 2, Kind: domain.EditDelete, By: "alice", At: sentAt}
	pin := domain.PinAction{Room: m.Room, Seq: m.Seq, PV: 1, Op: domain.PinOpPin, By: "bob", At: sentAt}
	for name, got := range map[string]*chatimv1.Message{
		"msg_deleted": pbconv.MessageDeleted(domain.RoomGroup, m, gone).GetMessageDeleted().GetMessage(),
		"msg_pinned":  pbconv.PinChanged(domain.RoomGroup, m, pin).GetMessagePinned().GetMessage(),
		"msg_created": pbconv.MessageCreated(domain.RoomGroup, m).GetMessageCreated().GetMessage(),
	} {
		if got.GetText() != "" || got.GetMentionTargets() != nil || got.GetMentionAll() || got.GetForwardFrom() != nil || got.GetReplyCount() != nil {
			t.Fatalf("%s carries content of a deleted message: %v", name, got)
		}
		if !proto.Equal(got.GetReplyTo(), &chatimv1.ReplyRef{Seq: 40}) || !got.GetDeleted() {
			t.Fatalf("%s = %v, want the reply link and deleted kept", name, got)
		}
	}
}
