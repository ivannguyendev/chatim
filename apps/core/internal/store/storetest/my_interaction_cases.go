package storetest

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func myInteractionCases() []interactionCase {
	return []interactionCase{
		{"my interactions return the caller's live emoji and bookmark per asked message", myInteractionsLive},
		{"my interactions of no message are empty", myInteractionsEmpty},
		{"my interactions reject a bad user or key", myInteractionsInvalid},
	}
}

func myInteractionsLive(t *testing.T, s interactionStores) {
	x := s.interactions
	setAll(t, x,
		reactionOf(roomA, mainThread, 1, "alice", "👍"), reactAt(roomA, mainThread, 1, "alice", "❤️", time.Second),
		reactionOf(roomA, mainThread, 2, "alice", "😂"), reactionOf(roomA, mainThread, 4, "bob", "😮"),
		reactionOf(roomA, mainThread, 5, "alice", "👍"), reactionOf(roomA, sideThread, 1, "alice", "😮"),
		reactionOf(roomB, mainThread, 1, "alice", "👍"),
	)
	mustRemove(t, x, msgKey(roomA, mainThread, 2), "alice", baseTime.Add(time.Second), true)
	mustSetBookmark(t, x, bookmarkAt(roomA, mainThread, 1, "alice", true, 0), true)
	mustSetBookmark(t, x, bookmarkAt(roomA, mainThread, 3, "alice", true, 0), true)
	mustSetBookmark(t, x, bookmarkAt(roomA, mainThread, 3, "alice", false, time.Second), true)
	mustSetBookmark(t, x, bookmarkAt(roomA, mainThread, 4, "bob", true, 0), true)
	mustSetBookmark(t, x, bookmarkAt(roomA, mainThread, 6, "alice", true, 0), true)
	asked := []store.MsgKey{
		msgKey(roomA, mainThread, 1), msgKey(roomA, mainThread, 2), msgKey(roomA, mainThread, 3), msgKey(roomA, mainThread, 4),
		msgKey(roomA, mainThread, 6), msgKey(roomA, mainThread, 9), msgKey(roomA, sideThread, 1),
	}
	got, err := x.MyInteractions(context.Background(), "alice", asked)
	if err != nil {
		t.Fatalf("MyInteractions: %v", err)
	}
	want := map[store.MsgKey]store.MyInteraction{
		msgKey(roomA, mainThread, 1): {Reaction: "❤️", Bookmarked: true},
		msgKey(roomA, mainThread, 6): {Bookmarked: true},
		msgKey(roomA, sideThread, 1): {Reaction: "😮"},
	}
	if !maps.Equal(got, want) {
		t.Fatalf("MyInteractions = %v, want %v", got, want)
	}
}

func myInteractionsEmpty(t *testing.T, s interactionStores) {
	mustSet(t, s.interactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
	got, err := s.interactions.MyInteractions(context.Background(), "alice", nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("MyInteractions(nil) = %v, %v; want empty", got, err)
	}
}

func myInteractionsInvalid(t *testing.T, s interactionStores) {
	cases := map[string]struct {
		user string
		keys []store.MsgKey
	}{
		"empty user": {"", []store.MsgKey{msgKey(roomA, mainThread, 1)}},
		"no room":    {"alice", []store.MsgKey{msgKey(0, mainThread, 1)}},
		"seq zero":   {"alice", []store.MsgKey{msgKey(roomA, mainThread, 0)}},
		"user has /": {"a/b", []store.MsgKey{msgKey(roomA, mainThread, 1)}},
		"second key": {"alice", []store.MsgKey{msgKey(roomA, mainThread, 1), msgKey(roomA, mainThread, 0)}},
	}
	for name, c := range cases {
		if _, err := s.interactions.MyInteractions(context.Background(), c.user, c.keys); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: MyInteractions error = %v, want invalid argument", name, err)
		}
	}
}
