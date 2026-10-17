package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func reactorCases() []interactionCase {
	return []interactionCase{
		{"reactors of one emoji list live reactions newest first, ties by user, through a cursor", reactorsOfEmoji},
		{"reactors of every emoji list by emoji then newest first through a cursor", reactorsOfAll},
		{"invalid reactor queries are rejected", reactorsInvalid},
	}
}

func seedReactors(t *testing.T, s interactionStores) store.MsgKey {
	t.Helper()
	setAll(t, s.interactions,
		reactAt(roomA, mainThread, 1, "alice", "👍", time.Second), reactAt(roomA, mainThread, 1, "carol", "👍", 0),
		reactAt(roomA, mainThread, 1, "dan", "👍", 3*time.Second), reactAt(roomA, mainThread, 1, "bob", "👍", 3*time.Second),
		reactAt(roomA, mainThread, 1, "erin", "😂", time.Second),
		reactAt(roomA, mainThread, 2, "frank", "👍", time.Second), reactAt(roomA, sideThread, 1, "gus", "👍", time.Second),
	)
	mustSet(t, s.interactions, reactAt(roomA, mainThread, 1, "carol", "❤️", 2*time.Second), true)
	key := msgKey(roomA, mainThread, 1)
	mustRemove(t, s.interactions, key, "erin", baseTime.Add(5*time.Second), true)
	return key
}

func reactor(user, emoji string, after time.Duration) store.Reactor {
	return store.Reactor{User: user, Emoji: emoji, At: baseTime.Add(after)}
}

func reactorsOfEmoji(t *testing.T, s interactionStores) {
	key := seedReactors(t, s)
	q := store.ReactorQuery{Key: key, Emoji: "👍", Limit: 10}
	bob, dan, alice := reactor("bob", "👍", 3*time.Second), reactor("dan", "👍", 3*time.Second), reactor("alice", "👍", time.Second)
	assertReactors(t, s, q, bob, dan, alice)
	q.Limit = 2
	assertReactors(t, s, q, bob, dan)
	q.After = q.Cursor(dan)
	assertReactors(t, s, q, alice)
	q.After = q.Cursor(alice)
	assertReactors(t, s, q)
	assertReactors(t, s, store.ReactorQuery{Key: key, Emoji: "😂", Limit: 10})
	assertReactors(t, s, store.ReactorQuery{Key: msgKey(roomA, sideThread, 1), Emoji: "👍", Limit: 10}, reactor("gus", "👍", time.Second))
}

func reactorsOfAll(t *testing.T, s interactionStores) {
	key := seedReactors(t, s)
	carol := reactor("carol", "❤️", 2*time.Second)
	bob, dan, alice := reactor("bob", "👍", 3*time.Second), reactor("dan", "👍", 3*time.Second), reactor("alice", "👍", time.Second)
	q := store.ReactorQuery{Key: key, Limit: 10}
	assertReactors(t, s, q, carol, bob, dan, alice)
	q.Limit = 2
	assertReactors(t, s, q, carol, bob)
	q.After = q.Cursor(bob)
	assertReactors(t, s, q, dan, alice)
	q.After, q.Limit = q.Cursor(carol), 1
	assertReactors(t, s, q, bob)
	assertReactors(t, s, store.ReactorQuery{Key: msgKey(roomA, mainThread, 9), Limit: 10})
}

func reactorsInvalid(t *testing.T, s interactionStores) {
	key := msgKey(roomA, mainThread, 1)
	for name, q := range map[string]store.ReactorQuery{
		"zero seq":             {Key: msgKey(roomA, mainThread, 0), Limit: 10},
		"zero limit":           {Key: key},
		"limit too big":        {Key: key, Limit: store.MaxPageLimit + 1},
		"cursor without user":  {Key: key, Limit: 10, After: store.ReactorCursor{At: baseTime}},
		"cursor without time":  {Key: key, Emoji: "👍", Limit: 10, After: store.ReactorCursor{User: "bob"}},
		"bad cursor user":      {Key: key, Emoji: "👍", Limit: 10, After: store.ReactorCursor{At: baseTime, User: "a.b"}},
		"all without an emoji": {Key: key, Limit: 10, After: store.ReactorCursor{At: baseTime, User: "bob"}},
	} {
		_, err := s.interactions.Reactors(t.Context(), q)
		assertErrorIs(t, "Reactors("+name+")", err, apperr.ErrInvalidArgument)
	}
}

func assertReactors(t *testing.T, s interactionStores, q store.ReactorQuery, want ...store.Reactor) {
	t.Helper()
	got, err := s.interactions.Reactors(t.Context(), q)
	same := func(a, b store.Reactor) bool { return a.User == b.User && a.Emoji == b.Emoji && a.At.Equal(b.At) }
	if err != nil || !slices.EqualFunc(got, want, same) {
		t.Fatalf("Reactors(%q after %+v, limit %d) = %+v, %v;\nwant %+v", q.Emoji, q.After, q.Limit, got, err, want)
	}
}
