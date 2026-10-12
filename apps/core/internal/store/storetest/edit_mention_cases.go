package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func editMentionCases() []editCase {
	return []editCase{
		{"facts keep their mention targets and @all", factsKeepMentions},
		{"apply edit replaces mentions with the fact's and an empty fact clears them", applyEditMentions},
	}
}

func mentioning(e domain.Edit) domain.Edit {
	e.Mentions, e.MentionAll = []domain.MentionTarget{mentionMinh, mentionOps}, true
	return e
}

func factsKeepMentions(t *testing.T, s editStores) {
	v1, v2 := mentioning(fact(roomA, mainThread, 1, 1)), fact(roomA, mainThread, 1, 2)
	mustAppend(t, s.edits, v1, v2)
	assertEditAt(t, s.edits, v1)
	assertEditAt(t, s.edits, v2)
}

func applyEditMentions(t *testing.T, s editStores) {
	m := linked(1)
	mustInsert(t, s.msgs, []domain.Message{m})
	v1 := mentioning(fact(roomA, mainThread, 1, 1))
	v1.Mentions, v1.MentionAll = []domain.MentionTarget{mentionLan}, false
	mustApply(t, s.msgs, v1)
	assertStored(t, s.msgs, edited(m, v1))
	v2 := mentioning(fact(roomA, mainThread, 1, 2))
	mustApply(t, s.msgs, v2)
	assertStored(t, s.msgs, edited(m, v2))
	v3 := fact(roomA, mainThread, 1, 3)
	mustApply(t, s.msgs, v3)
	if got := edited(m, v3); got.Mentions != nil || got.MentionAll || got.ReplyTo == nil || got.Forward == nil {
		t.Fatalf("fixture %+v must drop mentions and keep the reply and forward links", got)
	}
	assertStored(t, s.msgs, edited(m, v3))
}
