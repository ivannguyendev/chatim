package pbconv_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var addedAt = sentAt.Add(time.Minute)

func stampedTargets() []domain.MentionTarget {
	return []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh", At: sentAt}, {Kind: domain.MentionGroup, ID: "ops", At: addedAt}}
}

func wantStampedTargets() []*chatimv1.MentionTarget {
	return []*chatimv1.MentionTarget{
		{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "minh", At: timestamppb.New(sentAt)},
		{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "ops", At: timestamppb.New(addedAt)},
	}
}

func TestMessageCarriesMentionTimes(t *testing.T) {
	m := sample()
	m.Mentions, m.MentionAll, m.MentionAllAt = stampedTargets(), true, addedAt
	got := pbconv.Message(m)
	want := &chatimv1.Message{MentionTargets: wantStampedTargets(), MentionAll: true, MentionAllAt: timestamppb.New(addedAt)}
	if !proto.Equal(&chatimv1.Message{MentionTargets: got.GetMentionTargets(), MentionAll: got.GetMentionAll(), MentionAllAt: got.GetMentionAllAt()}, want) {
		t.Fatalf("mentions = %v, want %v", got, want)
	}
	if plain := pbconv.Message(sample()); plain.GetMentionAllAt() != nil {
		t.Fatalf("a message without @all carries %v", plain.GetMentionAllAt())
	}
}

func TestMentionSetCarriesTimes(t *testing.T) {
	got := pbconv.MentionSet(domain.Mentions{Targets: stampedTargets(), All: true, AllAt: sentAt})
	want := &chatimv1.MentionSet{Targets: wantStampedTargets(), All: true, AllAt: timestamppb.New(sentAt)}
	if !proto.Equal(got, want) {
		t.Fatalf("MentionSet = %v, want %v", got, want)
	}
	if pbconv.MentionSet(domain.Mentions{}) != nil {
		t.Fatal("an empty set must stay nil")
	}
}

func TestDomainMentionTargetsKeepTimes(t *testing.T) {
	got := pbconv.DomainMentionTargets(wantStampedTargets())
	want := stampedTargets()
	if len(got) != 2 || got[0].Identity() != want[0].Identity() || !got[0].At.Equal(sentAt) || !got[1].At.Equal(addedAt) {
		t.Fatalf("DomainMentionTargets = %+v, want %+v", got, want)
	}
	untimed := pbconv.DomainMentionTargets([]*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "minh"}})
	if !untimed[0].At.IsZero() {
		t.Fatalf("a target without at = %v, want zero", untimed[0].At)
	}
}

func TestDomainMentionAllTime(t *testing.T) {
	if got := pbconv.DomainTime(timestamppb.New(addedAt)); !got.Equal(addedAt) {
		t.Fatalf("DomainTime = %v, want %v", got, addedAt)
	}
	if got := pbconv.DomainTime(nil); !got.IsZero() {
		t.Fatalf("DomainTime(nil) = %v, want zero", got)
	}
}

func TestChangeEventsCarryPreviousMentions(t *testing.T) {
	prev := domain.Mentions{Targets: stampedTargets(), All: true, AllAt: sentAt}
	want := &chatimv1.MentionSet{Targets: wantStampedTargets(), All: true, AllAt: timestamppb.New(sentAt)}
	edited, e := changeOf(domain.EditText)
	if got := pbconv.MessageChanged(domain.RoomGroup, edited, e, prev).GetMessageEdited().GetPreviousMentions(); !proto.Equal(got, want) {
		t.Fatalf("msg_edited previous_mentions = %v, want %v", got, want)
	}
	deleted, d := changeOf(domain.EditDelete)
	if got := pbconv.MessageChanged(domain.RoomGroup, deleted, d, prev).GetMessageDeleted().GetPreviousMentions(); !proto.Equal(got, want) {
		t.Fatalf("msg_deleted previous_mentions = %v, want %v", got, want)
	}
	if got := pbconv.MessageEdited(domain.RoomGroup, edited, e, domain.Mentions{}).GetMessageEdited().GetPreviousMentions(); got != nil {
		t.Fatalf("no previous targets = %v, want nil", got)
	}
}
