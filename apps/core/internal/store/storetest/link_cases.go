package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func linked(seq uint64) domain.Message {
	m := msg(roomA, mainThread, seq)
	m.ReplyTo = &domain.ReplyRef{Seq: 1}
	m.Forward = &domain.ForwardRef{Room: roomB, Seq: 9, Author: "lan", SentAt: baseTime}
	m.Mentions = []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh"}, {Kind: domain.MentionGroup, ID: "team-design"}}
	m.MentionAll = true
	return m
}

func applyDeleteDropsLinks(t *testing.T, s editStores) {
	m, edit := linked(2), linked(3)
	mustInsert(t, s.msgs, []domain.Message{msg(roomA, mainThread, 1), m, edit})
	gone, changed := deletion(roomA, mainThread, 2, 1), fact(roomA, mainThread, 3, 1)
	mustApply(t, s.msgs, gone, changed)
	assertStored(t, s.msgs, edited(m, gone), edited(edit, changed))
}

func insertKeepsLinks(t *testing.T, s store.Messages) {
	parent := msg(roomA, mainThread, 1)
	reply := linked(2)
	counted := reply
	counted.Replies = domain.ReplyCount{N: 4, Version: 4}
	mustInsert(t, s, []domain.Message{parent, counted})
	got, err := s.Find(t.Context(), roomA, []store.MsgKey{store.KeyOf(parent), store.KeyOf(reply)})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	assertMessages(t, got, []domain.Message{parent, reply})
}
