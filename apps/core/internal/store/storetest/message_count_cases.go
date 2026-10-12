package storetest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type CountableMessages interface {
	store.Messages
	store.MessageCounts
}

type countCase struct {
	name string
	run  func(t *testing.T, s CountableMessages)
}

func RunMessageCounts(t *testing.T, open func(t *testing.T) CountableMessages) {
	t.Helper()
	cases := append([]countCase{
		{"reaction deltas add, move and drop emojis in count order", reactionDeltas},
		{"reaction deltas share the summary version with set reactions", reactionDeltasThenCAS},
		{"a removal of an absent emoji reads as no count", reactionDeltaBelowZero},
		{"reaction deltas give the same counts in any order", reactionDeltasCommute},
		{"reaction deltas on a missing message create nothing", reactionDeltasMissing},
		{"reaction deltas need valid emojis and non-zero changes", reactionDeltasInvalid},
	}, replyCountCases()...)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func delta(emoji string, d int) store.EmojiDelta { return store.EmojiDelta{Emoji: emoji, Delta: d} }

func emojiCount(emoji string, n uint32) domain.ReactionCount {
	return domain.ReactionCount{Emoji: emoji, Count: n}
}

func mustAddReactions(t *testing.T, s CountableMessages, key store.MsgKey, want domain.ReactionSummary, deltas ...store.EmojiDelta) {
	t.Helper()
	got, err := s.AddReactionCounts(t.Context(), key, deltas)
	if err != nil || got.Version != want.Version || !slices.Equal(got.Counts, want.Counts) {
		t.Fatalf("AddReactionCounts(%v) = %+v, %v; want %+v", deltas, got, err, want)
	}
}

func reactionDeltas(t *testing.T, s CountableMessages) {
	m, other := msg(roomA, mainThread, 1), msg(roomA, mainThread, 2)
	mustInsert(t, s, []domain.Message{m, other})
	key := store.KeyOf(m)
	mustAddReactions(t, s, key, summary(1, emojiCount("👍", 1)), delta("👍", 1))
	mustAddReactions(t, s, key, summary(2, emojiCount("👍", 2), emojiCount("❤️", 1)), delta("❤️", 1), delta("👍", 1))
	mustAddReactions(t, s, key, summary(3, emojiCount("❤️", 2), emojiCount("👍", 1)), delta("👍", -1), delta("❤️", 1))
	mustAddReactions(t, s, key, summary(4, emojiCount("$e", 2), emojiCount("❤️", 2), emojiCount("👍", 1)), delta("$e", 2))
	mustAddReactions(t, s, key, summary(5, emojiCount("$e", 2), emojiCount("❤️", 2)), delta("👍", -1))
	assertStored(t, s, withReactions(m, summary(5, emojiCount("$e", 2), emojiCount("❤️", 2))), other)
	mustAddReactions(t, s, key, summary(6), delta("$e", -2), delta("❤️", -2))
	assertStored(t, s, withReactions(m, summary(6)), other)
}

func reactionDeltasThenCAS(t *testing.T, s CountableMessages) {
	m := msg(roomA, sideThread, 3)
	mustInsert(t, s, []domain.Message{m})
	key := store.KeyOf(m)
	mustAddReactions(t, s, key, summary(1, emojiCount("👍", 1)), delta("👍", 1))
	mustSetReactions(t, s, key, 0, summary(1, emojiCount("😮", 1)), false)
	mustSetReactions(t, s, key, 1, summary(2, emojiCount("😮", 1)), true)
	mustAddReactions(t, s, key, summary(3, emojiCount("👍", 1), emojiCount("😮", 1)), delta("👍", 1))
	assertStored(t, s, withReactions(m, summary(3, emojiCount("👍", 1), emojiCount("😮", 1))))
}

func reactionDeltaBelowZero(t *testing.T, s CountableMessages) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s, []domain.Message{m})
	key := store.KeyOf(m)
	mustAddReactions(t, s, key, summary(1), delta("👍", -1))
	mustAddReactions(t, s, key, summary(2, emojiCount("❤️", 1)), delta("👍", -1), delta("❤️", 1))
	mustAddReactions(t, s, key, summary(3), delta("❤️", -3))
	assertStored(t, s, withReactions(m, summary(3)))
}

func reactionDeltasCommute(t *testing.T, s CountableMessages) {
	upFirst, downFirst := msg(roomA, mainThread, 1), msg(roomA, mainThread, 2)
	mustInsert(t, s, []domain.Message{upFirst, downFirst})
	up, down := store.KeyOf(upFirst), store.KeyOf(downFirst)
	mustAddReactions(t, s, up, summary(1, emojiCount("👍", 1)), delta("👍", 1))
	mustAddReactions(t, s, up, summary(2), delta("👍", -1))
	mustAddReactions(t, s, down, summary(1), delta("👍", -1))
	mustAddReactions(t, s, down, summary(2), delta("👍", 1))
	assertStored(t, s, withReactions(upFirst, summary(2)), withReactions(downFirst, summary(2)))
	got, err := s.Find(t.Context(), roomA, []store.MsgKey{up, down})
	if err != nil || len(got) != 2 || got[0].Reactions.Unsettled != got[1].Reactions.Unsettled {
		t.Fatalf("Find = %+v, %v; want both orders stored alike", got, err)
	}
	mustAddReactions(t, s, up, summary(3, emojiCount("👍", 1)), delta("👍", 1))
	mustAddReactions(t, s, down, summary(3, emojiCount("👍", 1)), delta("👍", 1))
	mustAddReactions(t, s, down, summary(4, emojiCount("👍", 2), emojiCount("❤️", 1)), delta("❤️", 1), delta("👍", 1))
}

func reactionDeltasMissing(t *testing.T, s CountableMessages) {
	key := msgKey(roomA, mainThread, 1)
	_, err := s.AddReactionCounts(t.Context(), key, []store.EmojiDelta{delta("👍", 1)})
	assertErrorIs(t, "AddReactionCounts(missing)", err, domain.ErrMessageNotFound)
	if got, err := s.Find(t.Context(), roomA, []store.MsgKey{key}); err != nil || len(got) != 0 {
		t.Fatalf("Find after AddReactionCounts on a missing message = %+v, %v; want nothing", got, err)
	}
}

func reactionDeltasInvalid(t *testing.T, s CountableMessages) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s, []domain.Message{m})
	key := store.KeyOf(m)
	for name, c := range map[string]struct {
		key    store.MsgKey
		deltas []store.EmojiDelta
	}{
		"no deltas":  {key, nil},
		"zero delta": {key, []store.EmojiDelta{delta("👍", 0)}},
		"bad emoji":  {key, []store.EmojiDelta{delta("", 1)}},
		"duplicate":  {key, []store.EmojiDelta{delta("👍", 1), delta("👍", 1)}},
		"zero seq":   {msgKey(roomA, mainThread, 0), []store.EmojiDelta{delta("👍", 1)}},
	} {
		_, err := s.AddReactionCounts(t.Context(), c.key, c.deltas)
		assertErrorIs(t, "AddReactionCounts("+name+")", err, apperr.ErrInvalidArgument)
	}
	assertStored(t, s, m)
}
