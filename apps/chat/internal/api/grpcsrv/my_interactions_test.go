package grpcsrv_test

import (
	"context"
	"slices"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type mine struct {
	seq      uint64
	reaction string
	marked   bool
}

func minesOf(msgs []*chatimv1.Message) []mine {
	out := []mine{}
	for _, m := range msgs {
		out = append(out, mine{m.GetSeq(), m.GetMyReaction(), m.GetBookmarked()})
	}
	return out
}

func (rg *rig) react(t *testing.T, ctx context.Context, room string, seq uint64, emoji string) {
	t.Helper()
	if _, err := rg.client.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: room, Seq: seq, Emoji: emoji}); err != nil {
		t.Fatalf("ReactMessage seq %d: %v", seq, err)
	}
}

func (rg *rig) mark(t *testing.T, ctx context.Context, room string, seq uint64) {
	t.Helper()
	if _, err := rg.client.SetBookmark(ctx, &chatimv1.SetBookmarkRequest{RoomId: room, Seq: seq, On: true}); err != nil {
		t.Fatalf("SetBookmark seq %d: %v", seq, err)
	}
}

func TestReaderSeesOwnReactionAndBookmarkOnlyOnVisibleMessages(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for seq := uint64(1); seq <= 4; seq++ {
		rg.insertAt(t, room, seq, "m", sentAtSeq(seq))
	}
	rg.react(t, bob, room, 1, "👍")
	rg.mark(t, bob, room, 1)
	rg.mark(t, bob, room, 2)
	rg.react(t, bob, room, 3, "❤️")
	rg.react(t, alice, room, 4, "😂")
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 3}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	bobSees := []mine{{1, "👍", true}, {2, "", false}, {3, "", false}, {4, "", false}}
	history := func(ctx context.Context, anchor chatimv1.HistoryAnchor, seq uint64) []mine {
		t.Helper()
		resp, err := rg.client.GetHistory(ctx, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: anchor, Seq: seq})
		if err != nil {
			t.Fatalf("GetHistory: %v", err)
		}
		return minesOf(resp.GetMessages())
	}
	if got := history(bob, chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST, 0); !slices.Equal(got, bobSees) {
		t.Fatalf("bob history = %v, want %v", got, bobSees)
	}
	if got := history(bob, chatimv1.HistoryAnchor_HISTORY_ANCHOR_AROUND, 2); !slices.Equal(got, bobSees) {
		t.Fatalf("bob AROUND 2 = %v, want %v", got, bobSees)
	}
	aliceSees := []mine{{1, "", false}, {2, "", false}, {3, "", false}, {4, "😂", false}}
	if got := history(alice, chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, 0); !slices.Equal(got, aliceSees) {
		t.Fatalf("alice history = %v, want %v", got, aliceSees)
	}
	resp, err := rg.client.GetMessages(bob, &chatimv1.GetMessagesRequest{RoomId: room, Seqs: []uint64{3, 1}})
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if got, want := minesOf(resp.GetMessages()), []mine{{1, "👍", true}, {3, "", false}}; !slices.Equal(got, want) {
		t.Fatalf("bob GetMessages = %v, want %v", got, want)
	}
}

func TestRepliesCarryTheReadersOwnReactionAndBookmark(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	bob := as(t, "acme", "bob")
	rg.insertAt(t, room, 1, "parent", sentAtSeq(1))
	rg.insertReply(t, room, 2, 1)
	rg.insertReply(t, room, 3, 1)
	rg.react(t, bob, room, 3, "😮")
	rg.mark(t, bob, room, 3)
	rg.mark(t, bob, room, 1)
	resp, err := rg.client.GetReplies(bob, &chatimv1.GetRepliesRequest{RoomId: room, Seq: 1})
	if err != nil {
		t.Fatalf("GetReplies: %v", err)
	}
	if got, want := minesOf(resp.GetMessages()), []mine{{2, "", false}, {3, "😮", true}}; !slices.Equal(got, want) {
		t.Fatalf("bob replies = %v, want %v", got, want)
	}
	marks, err := rg.client.ListBookmarks(bob, &chatimv1.ListBookmarksRequest{})
	if err != nil {
		t.Fatalf("ListBookmarks: %v", err)
	}
	if len(marks.GetItems()) != 2 {
		t.Fatalf("ListBookmarks = %v, want two items", marks)
	}
	for _, b := range marks.GetItems() {
		if m := b.GetMessage(); m.GetMyReaction() != "" || m.GetBookmarked() {
			t.Fatalf("ListBookmarks message %d carries reader marks %q %v", m.GetSeq(), m.GetMyReaction(), m.GetBookmarked())
		}
	}
}
