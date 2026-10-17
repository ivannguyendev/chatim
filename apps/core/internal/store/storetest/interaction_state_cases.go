package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func interactionStateCases() []factStateCase {
	return []factStateCase{
		{"a reaction change flags bit 1 at the write time and the same emoji again flags nothing", reactionChangeFlags},
		{"a second change keeps the older flag time and only a clear of the current ver drops it", reactionClearByVer},
		{"removing a reaction flags its tombstone and a repeat removal flags nothing", reactionRemoveFlags},
		{"bookmark on and off each flag bit 1 and the same state again flags nothing", bookmarkFlags},
		{"reply docs are never flagged", repliesNeverFlagged},
	}
}

func reactionChangeFlags(t *testing.T, s FactStateStores) {
	r := reactionOf(roomA, mainThread, 4, "bob", "👍")
	before := time.Now()
	mustSet(t, s.Interactions, r, true)
	assertInteractionFlag(t, s, keys.ReactionKind, before, time.Now())
	mustClearStates(t, s.states(), interactionState(keys.ReactionKind, "bob", 1))
	mustSet(t, s.Interactions, r, false)
	assertNoPending(t, s.states(), store.StateInteractions)
	r.Emoji = "❤️"
	before = time.Now()
	if got := mustSet(t, s.Interactions, r, true); got.N != 2 {
		t.Fatalf("Set(other emoji) ver = %d, want 2", got.N)
	}
	assertInteractionFlag(t, s, keys.ReactionKind, before, time.Now())
}

func reactionClearByVer(t *testing.T, s FactStateStores) {
	r := reactionOf(roomA, mainThread, 4, "bob", "👍")
	before := time.Now()
	mustSet(t, s.Interactions, r, true)
	first := assertInteractionFlag(t, s, keys.ReactionKind, before, time.Now())
	r.Emoji = "❤️"
	mustSet(t, s.Interactions, r, true)
	assertPendingDoc(t, onlyInteractionFlag(t, s), first)
	mustClearStates(t, s.states(), interactionState(keys.ReactionKind, "bob", 1))
	assertPendingDoc(t, onlyInteractionFlag(t, s), first)
	mustClearStates(t, s.states(), interactionState(keys.ReactionKind, "bob", 2))
	assertNoPending(t, s.states(), store.StateInteractions)
}

func reactionRemoveFlags(t *testing.T, s FactStateStores) {
	key := msgKey(roomA, mainThread, 4)
	mustSet(t, s.Interactions, reactionOf(roomA, mainThread, 4, "bob", "👍"), true)
	mustClearStates(t, s.states(), interactionState(keys.ReactionKind, "bob", 1))
	before := time.Now()
	if got := mustRemove(t, s.Interactions, key, "bob", secs(5), true); got.N != 2 {
		t.Fatalf("Remove ver = %d, want 2", got.N)
	}
	assertInteractionFlag(t, s, keys.ReactionKind, before, time.Now())
	mustClearStates(t, s.states(), interactionState(keys.ReactionKind, "bob", 2))
	mustRemove(t, s.Interactions, key, "bob", secs(6), false)
	assertNoPending(t, s.states(), store.StateInteractions)
}

func bookmarkFlags(t *testing.T, s FactStateStores) {
	for i, on := range []bool{true, false} {
		ver := uint64(i + 1)
		before := time.Now()
		mustSetBookmark(t, s.Interactions, bookmarkAt(roomA, mainThread, 4, "bob", on, time.Duration(i)*time.Second), true)
		assertInteractionFlag(t, s, keys.BookmarkKind, before, time.Now())
		mustClearStates(t, s.states(), interactionState(keys.BookmarkKind, "bob", ver))
		mustSetBookmark(t, s.Interactions, bookmarkAt(roomA, mainThread, 4, "bob", on, time.Hour), false)
		assertNoPending(t, s.states(), store.StateInteractions)
	}
}

func repliesNeverFlagged(t *testing.T, s FactStateStores) {
	first, second := replyAt(roomA, 4, 7, "lan", 0), replyAt(roomA, 4, 8, "lan", time.Second)
	mustAddReply(t, s.Interactions, first, true)
	mustRemoveReply(t, s.Interactions, first, true)
	mustRemoveReply(t, s.Interactions, second, false)
	mustAddReply(t, s.Interactions, second, false)
	assertNoPending(t, s.states(), store.StateInteractions)
}

func onlyInteractionFlag(t *testing.T, s FactStateStores) store.PendingDoc {
	t.Helper()
	got := allPending(t, s.states(), store.StateInteractions)
	if len(got) != 1 {
		t.Fatalf("Pending(interactions) = %+v, want one doc", got)
	}
	return got[0]
}

func assertInteractionFlag(t *testing.T, s FactStateStores, kind keys.InteractionKind, before, after time.Time) store.PendingDoc {
	t.Helper()
	got := onlyInteractionFlag(t, s)
	want := store.PendingDoc{Collection: store.StateInteractions, Room: roomA, Seq: 4, User: "bob", Kind: kind, State: store.BitInteraction, StateAt: got.StateAt}
	assertPendingDoc(t, got, want)
	if !aroundWrite(got.StateAt, before, after) {
		t.Fatalf("flag time %v, want between %v and %v", got.StateAt, before, after)
	}
	return got
}
