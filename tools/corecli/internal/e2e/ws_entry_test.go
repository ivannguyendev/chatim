package e2e_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lxzan/gws"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func wsMessage() *chatimv1.Message {
	return &chatimv1.Message{
		RoomId: "77", Seq: 1, Sender: "ws-u", Text: "edited", Ver: 1, MyReaction: "👍", Bookmarked: true,
		Reactions: &chatimv1.ReactionSummary{Counts: []*chatimv1.ReactionCount{{Emoji: "👍", Count: 1}}, Ver: 1},
	}
}

func TestCheckWSMessageAcceptsTheWantedState(t *testing.T) {
	want := e2e.WSMessage{Seq: 1, Sender: "ws-u", Text: "edited", Ver: 1, Emoji: "👍", Bookmarked: true}
	if err := e2e.CheckWSMessage(wsMessage(), want); err != nil {
		t.Fatalf("CheckWSMessage = %v", err)
	}
}

func TestCheckWSMessageRefusesEachDifference(t *testing.T) {
	base := e2e.WSMessage{Seq: 1, Sender: "ws-u", Text: "edited", Ver: 1, Emoji: "👍", Bookmarked: true}
	cases := map[string]func(*e2e.WSMessage){
		"seq":        func(w *e2e.WSMessage) { w.Seq = 2 },
		"sender":     func(w *e2e.WSMessage) { w.Sender = "other" },
		"text":       func(w *e2e.WSMessage) { w.Text = "sent" },
		"ver":        func(w *e2e.WSMessage) { w.Ver = 0 },
		"emoji":      func(w *e2e.WSMessage) { w.Emoji = "❤️" },
		"bookmarked": func(w *e2e.WSMessage) { w.Bookmarked = false },
	}
	for name, change := range cases {
		want := base
		change(&want)
		if err := e2e.CheckWSMessage(wsMessage(), want); err == nil {
			t.Fatalf("CheckWSMessage with another %s = nil, want an error", name)
		}
	}
	if err := e2e.CheckWSMessage(nil, base); err == nil {
		t.Fatal("CheckWSMessage of no message = nil, want an error")
	}
}

func TestCheckWSMessageWithoutEmojiWantsNoReactions(t *testing.T) {
	m := &chatimv1.Message{Seq: 1, Sender: "ws-u", Text: "sent"}
	if err := e2e.CheckWSMessage(m, e2e.WSMessage{Seq: 1, Sender: "ws-u", Text: "sent"}); err != nil {
		t.Fatalf("CheckWSMessage = %v", err)
	}
	if err := e2e.CheckWSMessage(wsMessage(), e2e.WSMessage{Seq: 1, Sender: "ws-u", Text: "edited", Ver: 1, Bookmarked: true}); err == nil {
		t.Fatal("CheckWSMessage with an unexpected reaction = nil, want an error")
	}
}

func TestMessageAtFindsTheSeq(t *testing.T) {
	msgs := []*chatimv1.Message{{Seq: 1}, {Seq: 2, Text: "two"}}
	if got := e2e.MessageAt(msgs, 2); got.GetText() != "two" {
		t.Fatalf("MessageAt 2 = %v", got)
	}
	if got := e2e.MessageAt(msgs, 3); got != nil {
		t.Fatalf("MessageAt 3 = %v, want nil", got)
	}
}

func TestSameReplyComparesProtos(t *testing.T) {
	a := &chatimv1.GetHistoryResponse{Messages: []*chatimv1.Message{wsMessage()}}
	b := &chatimv1.GetHistoryResponse{Messages: []*chatimv1.Message{wsMessage()}}
	if err := e2e.SameReply("history", a, b); err != nil {
		t.Fatalf("SameReply = %v", err)
	}
	b.Messages[0].Text = "other"
	if err := e2e.SameReply("history", a, b); err == nil || !strings.Contains(err.Error(), "history") {
		t.Fatalf("SameReply of different replies = %v, want an error naming history", err)
	}
}

func TestBookmarkedFindsTheMessage(t *testing.T) {
	items := []*chatimv1.BookmarkItem{{Message: &chatimv1.Message{RoomId: "77", Seq: 1}, Available: true}}
	if !e2e.Bookmarked(items, "77", 1) {
		t.Fatal("Bookmarked 77/1 = false")
	}
	if e2e.Bookmarked(items, "77", 2) || e2e.Bookmarked(items, "78", 1) {
		t.Fatal("Bookmarked found another message")
	}
	gone := []*chatimv1.BookmarkItem{{Message: &chatimv1.Message{RoomId: "77", Seq: 1}}}
	if e2e.Bookmarked(gone, "77", 1) {
		t.Fatal("Bookmarked counted an unavailable item")
	}
}

func TestCloseCodeReadsGwsErrors(t *testing.T) {
	if code, ok := e2e.CloseCode(&gws.CloseError{Code: 4002}); !ok || code != 4002 {
		t.Fatalf("CloseCode = %d, %v; want 4002", code, ok)
	}
	if _, ok := e2e.CloseCode(errors.New("reset")); ok {
		t.Fatal("CloseCode of a plain error = ok")
	}
}

func TestCheckAuthTimeoutWantsTheCodeAfterTheTimeout(t *testing.T) {
	if err := e2e.CheckAuthTimeout(4002, 5100*time.Millisecond, 5*time.Second); err != nil {
		t.Fatalf("CheckAuthTimeout = %v", err)
	}
	if err := e2e.CheckAuthTimeout(4001, 5100*time.Millisecond, 5*time.Second); err == nil {
		t.Fatal("CheckAuthTimeout with 4001 = nil")
	}
	if err := e2e.CheckAuthTimeout(4002, time.Second, 5*time.Second); err == nil {
		t.Fatal("CheckAuthTimeout closed early = nil")
	}
	if err := e2e.CheckAuthTimeout(4002, 20*time.Second, 5*time.Second); err == nil {
		t.Fatal("CheckAuthTimeout closed late = nil")
	}
}

func TestWriteTokensKeepsThemPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws-tokens")
	if err := e2e.WriteTokens(path, "a.b.c", "d.e.f"); err != nil {
		t.Fatalf("WriteTokens = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "a.b.c\nd.e.f\n" {
		t.Fatalf("tokens file = %q", data)
	}
	if err := e2e.WriteTokens(path); err == nil {
		t.Fatal("WriteTokens of no token = nil, want an error")
	}
}
