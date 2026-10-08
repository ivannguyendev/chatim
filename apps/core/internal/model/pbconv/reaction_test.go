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

var reactedAt = sentAt.Add(2 * time.Minute)

func counts(c ...domain.ReactionCount) domain.ReactionSummary {
	return domain.ReactionSummary{Counts: c, Version: 3}
}

func TestReactionSummary(t *testing.T) {
	if got := pbconv.ReactionSummary(domain.ReactionSummary{}); got != nil {
		t.Fatalf("ReactionSummary(zero) = %v, want nil", got)
	}
	s := counts(domain.ReactionCount{Emoji: "👍", Count: 2}, domain.ReactionCount{Emoji: "$e", Count: 1})
	want := &chatimv1.ReactionSummary{Counts: []*chatimv1.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "$e", Count: 1}}, Ver: 3}
	if got := pbconv.ReactionSummary(s); !proto.Equal(got, want) {
		t.Fatalf("ReactionSummary = %v, want %v", got, want)
	}
	cleared := pbconv.ReactionSummary(domain.ReactionSummary{Version: 4})
	if cleared == nil || len(cleared.GetCounts()) != 0 || cleared.GetVer() != 4 {
		t.Fatalf("ReactionSummary(all removed) = %v, want version 4 and no counts", cleared)
	}
}

func TestMessageCarriesReactionCounts(t *testing.T) {
	m := sample()
	m.Reactions = counts(domain.ReactionCount{Emoji: "👍", Count: 2})
	if got := pbconv.Message(m).GetReactions(); got == nil || !proto.Equal(got, pbconv.ReactionSummary(m.Reactions)) {
		t.Fatalf("Message reactions = %v, want %v", got, pbconv.ReactionSummary(m.Reactions))
	}
	if got := pbconv.Message(sample()).GetReactions(); got != nil {
		t.Fatalf("unreacted message carries reactions %v, want nil", got)
	}
}

func TestReactionChangedEnvelope(t *testing.T) {
	r := domain.Reaction{Room: 9_007_199_254_740_993, Seq: 7, Tenant: "acme", User: "bob", Emoji: "❤️", Prev: "👍", N: 2, At: reactedAt}
	want := &chatimv1.Event{
		Id: "9007199254740993-0-7-bob-n2", Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Seq: 7, Actor: "bob", Ts: timestamppb.New(reactedAt),
		Payload: &chatimv1.Event_ReactionChanged{ReactionChanged: &chatimv1.ReactionChanged{User: "bob", Emoji: "❤️", PreviousEmoji: "👍", Change: 2}},
	}
	if got := pbconv.ReactionChanged(domain.RoomGroup, r); !proto.Equal(got, want) {
		t.Fatalf("ReactionChanged = %v, want %v", got, want)
	}
}

func TestCountsChangedEnvelope(t *testing.T) {
	m := sample()
	m.Reactions = counts(domain.ReactionCount{Emoji: "👍", Count: 2})
	want := &chatimv1.Event{
		Id: "9007199254740993-0-7-reactions-v3", Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_DM,
		Seq: 7, Ts: timestamppb.New(reactedAt),
		Payload: &chatimv1.Event_CountsChanged{CountsChanged: &chatimv1.CountsChanged{Counter: pbconv.ReactionsCounter, Reactions: pbconv.ReactionSummary(m.Reactions)}},
	}
	if got := pbconv.CountsChanged(domain.RoomDM, m, reactedAt); !proto.Equal(got, want) {
		t.Fatalf("CountsChanged = %v, want %v", got, want)
	}
}
