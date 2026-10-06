package grpcsrv_test

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestHistoryReturnsReactionCountsButNotOnPlaceholders(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob", "carol")
	alice, bob, carol := as(t, "acme", "alice"), as(t, "acme", "bob"), as(t, "acme", "carol")
	for _, cid := range []string{"c-1", "c-2", "c-3"} {
		rg.send(t, alice, room, cid, "hi "+cid)
	}
	react := func(ctx context.Context, seq uint64, emoji string) {
		t.Helper()
		if _, err := rg.client.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: room, Seq: seq, Emoji: emoji}); err != nil {
			t.Fatalf("ReactMessage seq %d %q: %v", seq, emoji, err)
		}
	}
	react(bob, 1, "👍")
	react(carol, 1, "👍")
	react(alice, 1, "❤️")
	react(bob, 2, "🎉")
	react(bob, 3, "👀")
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 3}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	first := pbconv.ReactionSummary(domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}}, Version: 3})
	third := pbconv.ReactionSummary(domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👀", Count: 1}}, Version: 1})
	cases := []struct {
		name string
		ctx  context.Context
		want []*chatimv1.ReactionSummary
	}{
		{"bob", bob, []*chatimv1.ReactionSummary{first, nil, nil}},
		{"alice", alice, []*chatimv1.ReactionSummary{first, nil, third}},
	}
	for _, c := range cases {
		resp, err := rg.client.GetHistory(c.ctx, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST})
		if err != nil || len(resp.GetMessages()) != 3 {
			t.Fatalf("%s GetHistory = %v, %v; want 3 messages", c.name, resp, err)
		}
		for i, m := range resp.GetMessages() {
			if got := m.GetReactions(); !proto.Equal(got, c.want[i]) {
				t.Fatalf("%s message %d reactions = %v, want %v", c.name, m.GetSeq(), got, c.want[i])
			}
		}
	}
}
