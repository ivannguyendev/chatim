package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type ReactableMessages interface {
	store.Messages
	store.ReactionSummaries
}

type reactionStores struct {
	msgs      ReactableMessages
	reactions store.Reactions
}

type reactionCase struct {
	name string
	run  func(t *testing.T, s reactionStores)
}

func RunReactions(t *testing.T, open func(t *testing.T) (ReactableMessages, store.Reactions)) {
	t.Helper()
	for _, c := range slices.Concat(reactionSetCases(), reactionReadCases(), summaryCases()) {
		t.Run(c.name, func(t *testing.T) {
			msgs, reactions := open(t)
			c.run(t, reactionStores{msgs: msgs, reactions: reactions})
		})
	}
}

func reactionOf(room, thread, seq uint64, user, emoji string) domain.Reaction {
	return domain.Reaction{Room: room, Thread: thread, Seq: seq, Tenant: tenant, User: user, Emoji: emoji, At: baseTime}
}

func reactAt(room, thread, seq uint64, user, emoji string, after time.Duration) domain.Reaction {
	r := reactionOf(room, thread, seq, user, emoji)
	r.At = baseTime.Add(after)
	return r
}

func changed(r domain.Reaction, prev string, n uint32) domain.Reaction {
	r.Prev, r.N = prev, n
	return r
}

func mustSet(t *testing.T, s store.Reactions, r domain.Reaction, wantChanged bool) domain.Reaction {
	t.Helper()
	got, ok, err := s.Set(t.Context(), r)
	if err != nil || ok != wantChanged {
		t.Fatalf("Set(%q by %q on %d/%d/%d) = %+v, %v, %v; want changed %v", r.Emoji, r.User, r.Room, r.Thread, r.Seq, got, ok, err, wantChanged)
	}
	return got
}

func mustRemove(t *testing.T, s store.Reactions, key store.MsgKey, user string, at time.Time, wantChanged bool) domain.Reaction {
	t.Helper()
	got, ok, err := s.Remove(t.Context(), key, user, at)
	if err != nil || ok != wantChanged {
		t.Fatalf("Remove(%q on %+v) = %+v, %v, %v; want changed %v", user, key, got, ok, err, wantChanged)
	}
	return got
}

func sameReaction(a, b domain.Reaction) bool {
	at, bt := a.At, b.At
	a.At, b.At = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}

func assertReactions(t *testing.T, op string, got, want []domain.Reaction) {
	t.Helper()
	if !slices.EqualFunc(got, want, sameReaction) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func assertStoredReaction(t *testing.T, s store.Reactions, want domain.Reaction) {
	t.Helper()
	got, ok, err := s.Get(t.Context(), store.ReactionKeyOf(want), want.User)
	if err != nil || !ok {
		t.Fatalf("Get(%q on %+v) = %+v, %v, %v; want %+v", want.User, store.ReactionKeyOf(want), got, ok, err, want)
	}
	assertReactions(t, "Get", []domain.Reaction{got}, []domain.Reaction{want})
}

func assertNoReaction(t *testing.T, s store.Reactions, key store.MsgKey, user string) {
	t.Helper()
	if got, ok, err := s.Get(t.Context(), key, user); ok || err != nil {
		t.Fatalf("Get(%q on %+v) = %+v, %v, %v; want nothing", user, key, got, ok, err)
	}
}

func assertCounts(t *testing.T, s store.Reactions, key store.MsgKey, want []domain.ReactionCount) {
	t.Helper()
	got, err := s.Count(t.Context(), key, nil)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Count(%+v) = %v, %v; want %v", key, got, err, want)
	}
}
