package store_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestInteractionAndMessageCountKindsComeLast(t *testing.T) {
	if store.BookmarkChanged != 11 || store.MessageCountCheck != 12 {
		t.Fatalf("BookmarkChanged = %d, MessageCountCheck = %d; want 11 and 12", store.BookmarkChanged, store.MessageCountCheck)
	}
}

func TestReplyMentionFlagsOf(t *testing.T) {
	reply := &domain.ReplyRef{Seq: 4}
	user := []domain.MentionTarget{{Kind: domain.MentionUser, ID: "bob"}}
	cases := map[string]struct {
		m    domain.Message
		want store.ReplyMentionFlags
	}{
		"plain":            {domain.Message{}, 0},
		"reply":            {domain.Message{ReplyTo: reply}, 1},
		"mention":          {domain.Message{Mentions: user}, 2},
		"mention all":      {domain.Message{MentionAll: true}, 2},
		"reply and @all":   {domain.Message{ReplyTo: reply, MentionAll: true}, 3},
		"forward only":     {domain.Message{Forward: &domain.ForwardRef{Room: 9, Seq: 1}}, 0},
		"reply with users": {domain.Message{ReplyTo: reply, Mentions: user}, store.HasReply | store.HasMention},
	}
	for name, c := range cases {
		if got := store.ReplyMentionFlagsOf(c.m); got != c.want {
			t.Errorf("%s: ReplyMentionFlagsOf = %d, want %d", name, got, c.want)
		}
	}
}
