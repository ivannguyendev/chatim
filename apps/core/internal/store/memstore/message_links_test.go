package memstore_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

func TestMessagesNeverShareLinksWithCallers(t *testing.T) {
	s := memstore.NewMessages()
	m := domain.Message{
		Room: 7, Seq: 2, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-1", CreatedAt: time.Unix(1, 0),
		ReplyTo:  &domain.ReplyRef{Seq: 1},
		Forward:  &domain.ForwardRef{Room: 8, Seq: 9, Author: "lan"},
		Mentions: []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh"}},
	}
	s.Insert(t.Context(), []domain.Message{m})
	m.ReplyTo.Seq, m.Forward.Author, m.Mentions[0].ID = 5, "x", "x"
	read := func() domain.Message {
		got, err := s.Find(t.Context(), 7, []store.MsgKey{{Room: 7, Seq: 2}})
		if err != nil || len(got) != 1 {
			t.Fatalf("Find = %v, %v", got, err)
		}
		return got[0]
	}
	first := read()
	first.ReplyTo.Seq, first.Forward.Author, first.Mentions[0].ID = 6, "y", "y"
	page, err := s.Page(t.Context(), store.PageQuery{Room: 7, Anchor: store.Oldest, Limit: 10})
	if err != nil || len(page) != 1 {
		t.Fatalf("Page = %v, %v", page, err)
	}
	page[0].Mentions[0].ID = "z"
	got := read()
	if got.ReplyTo.Seq != 1 || got.Forward.Author != "lan" || got.Mentions[0].ID != "minh" {
		t.Fatalf("stored links changed through a caller: %+v %+v %+v", got.ReplyTo, got.Forward, got.Mentions)
	}
}
