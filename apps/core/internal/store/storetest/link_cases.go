package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func insertKeepsLinks(t *testing.T, s store.Messages) {
	parent := msg(roomA, mainThread, 1)
	reply := msg(roomA, mainThread, 2)
	reply.ReplyTo = &domain.ReplyRef{Seq: 1}
	reply.Forward = &domain.ForwardRef{Room: roomB, Seq: 9, Author: "lan", SentAt: baseTime}
	reply.Mentions = []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh"}, {Kind: domain.MentionGroup, ID: "team-design"}}
	reply.MentionAll = true
	counted := reply
	counted.Replies = domain.ReplyCount{N: 4, Version: 4}
	mustInsert(t, s, []domain.Message{parent, counted})
	got, err := s.Find(t.Context(), roomA, []store.MsgKey{store.KeyOf(parent), store.KeyOf(reply)})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	assertMessages(t, got, []domain.Message{parent, reply})
}
