package storetest

import (
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func summaryCases() []reactionCase {
	return []reactionCase{
		{"set reactions writes only over the expected version", summaryCAS},
		{"set reactions on a missing message writes nothing", summaryMissing},
		{"insert never stores a reaction summary", summaryNotInserted},
		{"set reactions needs a version above the base", summaryInvalid},
	}
}

func summary(version uint64, counts ...domain.ReactionCount) domain.ReactionSummary {
	return domain.ReactionSummary{Counts: counts, Version: version}
}

func withReactions(m domain.Message, s domain.ReactionSummary) domain.Message {
	m.Reactions = s
	return m
}

func mustSetReactions(t *testing.T, s store.ReactionSummaries, key store.MsgKey, base uint64, sum domain.ReactionSummary, want bool) {
	t.Helper()
	ok, err := s.SetReactions(t.Context(), key, base, sum)
	if err != nil || ok != want {
		t.Fatalf("SetReactions(%+v, base %d, v%d) = %v, %v; want %v", key, base, sum.Version, ok, err, want)
	}
}

func summaryCAS(t *testing.T, s reactionStores) {
	m, other := msg(roomA, mainThread, 1), msg(roomA, mainThread, 2)
	mustInsert(t, s.msgs, []domain.Message{m, other})
	key := store.KeyOf(m)
	first := summary(1, domain.ReactionCount{Emoji: "👍", Count: 2}, domain.ReactionCount{Emoji: "$e", Count: 1})
	mustSetReactions(t, s.msgs, key, 0, first, true)
	assertStored(t, s.msgs, withReactions(m, first), other)
	mustSetReactions(t, s.msgs, key, 0, summary(1, domain.ReactionCount{Emoji: "❤️", Count: 9}), false)
	mustSetReactions(t, s.msgs, key, 2, summary(3), false)
	assertStored(t, s.msgs, withReactions(m, first), other)
	second := summary(2, domain.ReactionCount{Emoji: "👍", Count: 3})
	mustSetReactions(t, s.msgs, key, 1, second, true)
	cleared := summary(3)
	mustSetReactions(t, s.msgs, key, 2, cleared, true)
	page, err := s.msgs.Page(t.Context(), store.PageQuery{Room: roomA, Thread: mainThread, Anchor: store.Latest, Limit: 10})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	assertMessages(t, page, []domain.Message{withReactions(m, cleared), other})
}

func summaryMissing(t *testing.T, s reactionStores) {
	key := msgKey(roomA, mainThread, 1)
	mustSetReactions(t, s.msgs, key, 0, summary(1, domain.ReactionCount{Emoji: "👍", Count: 1}), false)
	if got, err := s.msgs.Find(t.Context(), roomA, []store.MsgKey{key}); err != nil || len(got) != 0 {
		t.Fatalf("Find after SetReactions on a missing message = %+v, %v; want nothing", got, err)
	}
}

func summaryNotInserted(t *testing.T, s reactionStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{withReactions(m, summary(4, domain.ReactionCount{Emoji: "👍", Count: 1}))})
	assertStored(t, s.msgs, m)
	mustSetReactions(t, s.msgs, store.KeyOf(m), 0, summary(1, domain.ReactionCount{Emoji: "👍", Count: 1}), true)
}

func summaryInvalid(t *testing.T, s reactionStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	key := store.KeyOf(m)
	for name, c := range map[string]struct {
		key  store.MsgKey
		base uint64
		sum  domain.ReactionSummary
	}{
		"same version":            {key, 1, summary(1)},
		"lower version":           {key, 2, summary(1)},
		"zero version":            {key, 0, summary(0)},
		"version above max int64": {key, 0, summary(math.MaxInt64 + 1)},
		"zero seq":                {msgKey(roomA, mainThread, 0), 0, summary(1)},
	} {
		_, err := s.msgs.SetReactions(t.Context(), c.key, c.base, c.sum)
		assertErrorIs(t, "SetReactions("+name+")", err, apperr.ErrInvalidArgument)
	}
	assertStored(t, s.msgs, m)
}
