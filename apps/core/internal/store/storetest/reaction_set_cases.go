package storetest

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func reactionSetCases() []reactionCase {
	return []reactionCase{
		{"set stores the emoji as change 1 with no previous emoji", reactSetFirst},
		{"set of the same emoji is a no-op that returns the stored reaction", reactSetSame},
		{"set of another emoji replaces it and keeps the previous one", reactSetOther},
		{"remove leaves a tombstone and a second remove is a no-op", reactRemove},
		{"remove without a reaction stores nothing", reactRemoveMissing},
		{"set after remove counts on from the tombstone", reactSetAfterRemove},
		{"invalid reactions and limits are rejected", reactInvalid},
	}
}

func reactSetFirst(t *testing.T, s reactionStores) {
	r := reactionOf(roomA, mainThread, 1, "alice", "👍")
	got := mustSet(t, s.reactions, r, true)
	want := changed(r, "", 1)
	assertReactions(t, "Set", []domain.Reaction{got}, []domain.Reaction{want})
	assertStoredReaction(t, s.reactions, want)
	assertNoReaction(t, s.reactions, msgKey(roomA, mainThread, 1), "bob")
	assertNoReaction(t, s.reactions, msgKey(roomA, mainThread, 2), "alice")
	assertNoReaction(t, s.reactions, msgKey(roomA, sideThread, 1), "alice")
}

func reactSetSame(t *testing.T, s reactionStores) {
	first := mustSet(t, s.reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
	got := mustSet(t, s.reactions, reactAt(roomA, mainThread, 1, "alice", "👍", time.Minute), false)
	assertReactions(t, "Set(same emoji)", []domain.Reaction{got}, []domain.Reaction{first})
	assertStoredReaction(t, s.reactions, first)
}

func reactSetOther(t *testing.T, s reactionStores) {
	mustSet(t, s.reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
	heart := reactAt(roomA, mainThread, 1, "alice", "❤️", time.Minute)
	got := mustSet(t, s.reactions, heart, true)
	want := changed(heart, "👍", 2)
	assertReactions(t, "Set(other emoji)", []domain.Reaction{got}, []domain.Reaction{want})
	assertStoredReaction(t, s.reactions, want)
}

func reactRemove(t *testing.T, s reactionStores) {
	set := mustSet(t, s.reactions, reactionOf(roomA, sideThread, 4, "alice", "👍"), true)
	key, at := store.ReactionKeyOf(set), baseTime.Add(time.Hour)
	got := mustRemove(t, s.reactions, key, "alice", at, true)
	want := set
	want.Emoji, want.Prev, want.N, want.At = "", "👍", 2, at
	assertReactions(t, "Remove", []domain.Reaction{got}, []domain.Reaction{want})
	again := mustRemove(t, s.reactions, key, "alice", at.Add(time.Hour), false)
	assertReactions(t, "Remove(again)", []domain.Reaction{again}, []domain.Reaction{want})
	assertStoredReaction(t, s.reactions, want)
}

func reactRemoveMissing(t *testing.T, s reactionStores) {
	key := msgKey(roomA, mainThread, 1)
	if got := mustRemove(t, s.reactions, key, "bob", baseTime, false); got != (domain.Reaction{}) {
		t.Fatalf("Remove(missing) = %+v, want the zero reaction", got)
	}
	assertNoReaction(t, s.reactions, key, "bob")
}

func reactSetAfterRemove(t *testing.T, s reactionStores) {
	key := msgKey(roomA, mainThread, 1)
	mustSet(t, s.reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
	mustRemove(t, s.reactions, key, "alice", baseTime.Add(time.Second), true)
	r := reactAt(roomA, mainThread, 1, "alice", "👍", time.Minute)
	got := mustSet(t, s.reactions, r, true)
	assertReactions(t, "Set(after remove)", []domain.Reaction{got}, []domain.Reaction{changed(r, "", 3)})
}

func reactInvalid(t *testing.T, s reactionStores) {
	for name, mutate := range map[string]func(*domain.Reaction){
		"zero room":         func(r *domain.Reaction) { r.Room = 0 },
		"zero seq":          func(r *domain.Reaction) { r.Seq = 0 },
		"user with a dot":   func(r *domain.Reaction) { r.User = "a.b" },
		"empty user":        func(r *domain.Reaction) { r.User = "" },
		"empty tenant":      func(r *domain.Reaction) { r.Tenant = "" },
		"empty emoji":       func(r *domain.Reaction) { r.Emoji = "" },
		"emoji of 33 bytes": func(r *domain.Reaction) { r.Emoji = strings.Repeat("a", 33) },
		"control emoji":     func(r *domain.Reaction) { r.Emoji = "a\nb" },
	} {
		r := reactionOf(roomA, mainThread, 1, "alice", "👍")
		mutate(&r)
		_, _, err := s.reactions.Set(t.Context(), r)
		assertErrorIs(t, "Set("+name+")", err, apperr.ErrInvalidArgument)
	}
	_, _, err := s.reactions.Remove(t.Context(), msgKey(roomA, mainThread, 0), "alice", baseTime)
	assertErrorIs(t, "Remove(zero seq)", err, apperr.ErrInvalidArgument)
	_, _, err = s.reactions.Remove(t.Context(), msgKey(roomA, mainThread, 1), "a.b", baseTime)
	assertErrorIs(t, "Remove(bad user)", err, apperr.ErrInvalidArgument)
	_, err = s.reactions.Count(t.Context(), msgKey(roomA, mainThread, 0), nil)
	assertErrorIs(t, "Count(zero seq)", err, apperr.ErrInvalidArgument)
	for _, limit := range []int{0, store.MaxReactionScan + 1} {
		_, err := s.reactions.Between(t.Context(), roomA, baseTime, baseTime.Add(time.Hour), limit)
		assertErrorIs(t, "Between(bad limit)", err, apperr.ErrInvalidArgument)
	}
	assertNoReaction(t, s.reactions, msgKey(roomA, mainThread, 1), "alice")
}
