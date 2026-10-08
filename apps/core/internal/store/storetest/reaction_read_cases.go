package storetest

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func reactionReadCases() []reactionCase {
	return []reactionCase{
		{"count returns live emojis of that message only, sorted by count then emoji", reactCount},
		{"count fails with a stale read while a witness is behind", reactWitness},
		{"between returns reactions and tombstones of a room by time then key", reactBetween},
		{"cancelled context writes nothing", reactCancelled},
	}
}

func setAll(t *testing.T, s store.Reactions, rs ...domain.Reaction) {
	t.Helper()
	for _, r := range rs {
		mustSet(t, s, r, true)
	}
}

func reactCount(t *testing.T, s reactionStores) {
	setAll(t, s.reactions,
		reactionOf(roomA, mainThread, 1, "alice", "👍"), reactionOf(roomA, mainThread, 1, "bob", "👍"),
		reactionOf(roomA, mainThread, 1, "carol", "❤️"), reactionOf(roomA, mainThread, 1, "dave", "😂"),
		reactionOf(roomA, mainThread, 1, "erin", "$e"), reactionOf(roomA, mainThread, 1, "frank", "a.b"),
		reactionOf(roomA, mainThread, 2, "alice", "👍"), reactionOf(roomA, sideThread, 1, "bob", "❤️"),
		reactionOf(roomB, mainThread, 1, "carol", "👍"),
	)
	key := msgKey(roomA, mainThread, 1)
	mustRemove(t, s.reactions, key, "dave", baseTime.Add(time.Second), true)
	assertCounts(t, s.reactions, key, []domain.ReactionCount{
		{Emoji: "👍", Count: 2}, {Emoji: "$e", Count: 1}, {Emoji: "a.b", Count: 1}, {Emoji: "❤️", Count: 1},
	})
	assertCounts(t, s.reactions, msgKey(roomA, sideThread, 1), []domain.ReactionCount{{Emoji: "❤️", Count: 1}})
	assertCounts(t, s.reactions, msgKey(roomA, mainThread, 9), nil)
	assertStoredReaction(t, s.reactions, changed(reactionOf(roomA, mainThread, 1, "erin", "$e"), "", 1))
}

func reactWitness(t *testing.T, s reactionStores) {
	key := msgKey(roomA, mainThread, 1)
	mustSet(t, s.reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
	mustSet(t, s.reactions, reactionOf(roomA, mainThread, 1, "bob", "❤️"), true)
	mustRemove(t, s.reactions, key, "bob", baseTime.Add(time.Second), true)
	got, err := s.reactions.Count(t.Context(), key, []store.Witness{{User: "alice", N: 1}, {User: "bob", N: 2}, {User: "alice", N: 1}})
	if err != nil || !slices.Equal(got, []domain.ReactionCount{{Emoji: "👍", Count: 1}}) {
		t.Fatalf("Count(witnessed) = %v, %v; want [{👍 1}]", got, err)
	}
	for name, ws := range map[string][]store.Witness{
		"behind":            {{User: "alice", N: 2}},
		"missing":           {{User: "carol", N: 1}},
		"one of two behind": {{User: "alice", N: 1}, {User: "bob", N: 3}},
	} {
		_, err := s.reactions.Count(t.Context(), key, ws)
		assertErrorIs(t, "Count("+name+")", err, store.ErrStaleRead)
		assertErrorIs(t, "Count("+name+")", err, apperr.ErrUnavailable)
	}
}

func reactBetween(t *testing.T, s reactionStores) {
	from, to := baseTime.Add(time.Second), baseTime.Add(3*time.Second)
	mustSet(t, s.reactions, reactAt(roomA, mainThread, 4, "alice", "👍", 0), true)
	atFrom := mustSet(t, s.reactions, reactAt(roomA, mainThread, 5, "alice", "👍", time.Second), true)
	lowKey := mustSet(t, s.reactions, reactAt(roomA, mainThread, 2, "bob", "👍", 2*time.Second), true)
	highBob := mustSet(t, s.reactions, reactAt(roomA, mainThread, 3, "bob", "👍", 2*time.Second), true)
	highCarol := mustSet(t, s.reactions, reactAt(roomA, mainThread, 3, "carol", "❤️", 2*time.Second), true)
	mustSet(t, s.reactions, reactAt(roomA, sideThread, 1, "dave", "😂", 0), true)
	gone := mustRemove(t, s.reactions, msgKey(roomA, sideThread, 1), "dave", to, true)
	mustSet(t, s.reactions, reactAt(roomA, mainThread, 1, "erin", "👍", 4*time.Second), true)
	other := mustSet(t, s.reactions, reactAt(roomB, mainThread, 1, "alice", "👍", 2*time.Second), true)
	want := []domain.Reaction{atFrom, lowKey, highBob, highCarol, gone}
	cases := []struct {
		room     uint64
		from, to time.Time
		limit    int
		want     []domain.Reaction
	}{
		{roomA, from, to, store.MaxReactionScan, want},
		{roomA, from, to, 2, want[:2]},
		{roomB, from, to, 10, []domain.Reaction{other}},
		{roomA, to.Add(time.Hour), to.Add(2 * time.Hour), 10, nil},
	}
	for _, c := range cases {
		got, err := s.reactions.Between(t.Context(), c.room, c.from, c.to, c.limit)
		if err != nil {
			t.Fatalf("Between(%d, limit %d): %v", c.room, c.limit, err)
		}
		assertReactions(t, fmt.Sprintf("Between(%d, limit %d)", c.room, c.limit), got, c.want)
	}
}

func reactCancelled(t *testing.T, s reactionStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	ctx, key := cancelledContext(t), store.KeyOf(m)
	_, _, err := s.reactions.Set(ctx, reactionOf(roomA, mainThread, 1, "alice", "👍"))
	assertErrorIs(t, "Set", err, context.Canceled)
	_, _, err = s.reactions.Remove(ctx, key, "alice", baseTime)
	assertErrorIs(t, "Remove", err, context.Canceled)
	_, _, err = s.reactions.Get(ctx, key, "alice")
	assertErrorIs(t, "Get", err, context.Canceled)
	_, err = s.reactions.Count(ctx, key, nil)
	assertErrorIs(t, "Count", err, context.Canceled)
	_, err = s.msgs.SetReactions(ctx, key, 0, domain.ReactionSummary{Version: 1})
	assertErrorIs(t, "SetReactions", err, context.Canceled)
	assertNoReaction(t, s.reactions, key, "alice")
	assertStored(t, s.msgs, m)
}
