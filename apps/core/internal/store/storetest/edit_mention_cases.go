package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func editMentionCases() []editCase {
	return []editCase{
		{"facts keep their mention targets and @all with their times", factsKeepMentions},
		{"a delete fact keeps the targets of the deleted version", deleteFactKeepsMentions},
		{"apply edit replaces mentions with the fact's and an empty fact clears them", applyEditMentions},
	}
}

func mentioning(e domain.Edit) domain.Edit {
	e.Mentions = []domain.MentionTarget{stampedAt(mentionMinh, baseTime), stampedAt(mentionOps, e.At)}
	e.MentionAll, e.MentionAllAt = true, baseTime
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
	v1.Mentions, v1.MentionAll, v1.MentionAllAt = []domain.MentionTarget{stampedAt(mentionLan, v1.At)}, false, time.Time{}
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

func deleteFactKeepsMentions(t *testing.T, s editStores) {
	m := linked(1)
	mustInsert(t, s.msgs, []domain.Message{m})
	gone := mentioning(deletion(roomA, mainThread, 1, 1))
	mustAppend(t, s.edits, gone)
	assertEditAt(t, s.edits, gone)
	mustApply(t, s.msgs, gone)
	assertStored(t, s.msgs, edited(m, gone))
}
