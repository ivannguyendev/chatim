package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func bookmarkCases() []interactionCase {
	return []interactionCase{
		{"bookmark again in the same state changes nothing and each toggle counts a version", bookmarkToggle},
		{"removing a bookmark that never existed stores nothing", bookmarkRemoveMissing},
		{"reaction, bookmark and reply on one message stay apart", interactionKindsApart},
		{"invalid bookmarks are rejected", bookmarkInvalid},
	}
}

func bookmarkToggle(t *testing.T, s interactionStores) {
	on := bookmarkAt(roomA, mainThread, 1, "alice", true, 0)
	first := mustSetBookmark(t, s.interactions, on, true)
	assertBookmarks(t, "SetBookmark(on)", []domain.Bookmark{first}, nil, versioned(on, 1))
	again := mustSetBookmark(t, s.interactions, bookmarkAt(roomA, mainThread, 1, "alice", true, time.Minute), false)
	assertBookmarks(t, "SetBookmark(on again)", []domain.Bookmark{again}, nil, first)
	assertStoredBookmark(t, s.interactions, first)
	off := bookmarkAt(roomA, mainThread, 1, "alice", false, 2*time.Minute)
	removed := mustSetBookmark(t, s.interactions, off, true)
	assertBookmarks(t, "SetBookmark(off)", []domain.Bookmark{removed}, nil, versioned(off, 2))
	mustSetBookmark(t, s.interactions, bookmarkAt(roomA, mainThread, 1, "alice", false, 3*time.Minute), false)
	assertStoredBookmark(t, s.interactions, versioned(off, 2))
	back := bookmarkAt(roomA, mainThread, 1, "alice", true, 4*time.Minute)
	assertBookmarks(t, "SetBookmark(on after off)", []domain.Bookmark{mustSetBookmark(t, s.interactions, back, true)}, nil, versioned(back, 3))
	assertStoredBookmark(t, s.interactions, versioned(back, 3))
}

func bookmarkRemoveMissing(t *testing.T, s interactionStores) {
	if got := mustSetBookmark(t, s.interactions, bookmarkAt(roomA, mainThread, 1, "bob", false, 0), false); got != (domain.Bookmark{}) {
		t.Fatalf("SetBookmark(off, missing) = %+v, want the zero bookmark", got)
	}
	if got, ok, err := s.interactions.GetBookmark(t.Context(), msgKey(roomA, mainThread, 1), "bob"); ok || err != nil {
		t.Fatalf("GetBookmark(missing) = %+v, %v, %v; want nothing", got, ok, err)
	}
}

func interactionKindsApart(t *testing.T, s interactionStores) {
	key := msgKey(roomA, mainThread, 1)
	reaction := mustSet(t, s.interactions, reactAt(roomA, mainThread, 1, "alice", "👍", time.Second), true)
	bookmark := mustSetBookmark(t, s.interactions, bookmarkAt(roomA, mainThread, 1, "alice", true, 2*time.Second), true)
	reply := replyAt(roomA, 1, 2, "alice", 3*time.Second)
	mustAddReply(t, s.interactions, reply, true)
	assertStoredReaction(t, s.interactions, reaction)
	assertStoredBookmark(t, s.interactions, bookmark)
	assertCounts(t, s.interactions, key, []domain.ReactionCount{{Emoji: "👍", Count: 1}})
	assertReplies(t, s.interactions, key, 0, 10, live(reply))
	gone := mustRemove(t, s.interactions, key, "alice", baseTime.Add(4*time.Second), true)
	assertStoredBookmark(t, s.interactions, bookmark)
	assertLiveReplies(t, s.interactions, key, 1)
	off := mustSetBookmark(t, s.interactions, bookmarkAt(roomA, mainThread, 1, "alice", false, 5*time.Second), true)
	assertStoredReaction(t, s.interactions, gone)
	assertCounts(t, s.interactions, key, nil)
	assertReplies(t, s.interactions, key, 0, 10, live(reply))
	from, to := baseTime, baseTime.Add(time.Hour)
	for kind, want := range map[keys.InteractionKind][]store.Interaction{
		keys.ReactionKind: reactionRefs(gone),
		keys.BookmarkKind: {{Kind: keys.BookmarkKind, Key: key, User: "alice", Ver: 2, At: off.At}},
		keys.ReplyKind:    {{Kind: keys.ReplyKind, Key: key, User: "alice", Ver: 1, At: reply.At, Reply: store.ReplyKeyOf(reply)}},
	} {
		got, err := s.interactions.Between(t.Context(), roomA, kind, from, to, 10)
		if err != nil {
			t.Fatalf("Between(kind %d): %v", kind, err)
		}
		assertInteractions(t, "Between", got, want)
	}
}

func bookmarkInvalid(t *testing.T, s interactionStores) {
	for name, mutate := range map[string]func(*domain.Bookmark){
		"zero room":       func(b *domain.Bookmark) { b.Room = 0 },
		"zero seq":        func(b *domain.Bookmark) { b.Seq = 0 },
		"user with a dot": func(b *domain.Bookmark) { b.User = "a.b" },
		"empty tenant":    func(b *domain.Bookmark) { b.Tenant = "" },
		"zero time":       func(b *domain.Bookmark) { b.At = time.Time{} },
	} {
		for _, on := range []bool{true, false} {
			b := bookmarkAt(roomA, mainThread, 1, "alice", on, 0)
			mutate(&b)
			_, _, err := s.interactions.SetBookmark(t.Context(), b)
			assertErrorIs(t, "SetBookmark("+name+")", err, apperr.ErrInvalidArgument)
		}
	}
	if got, ok, err := s.interactions.GetBookmark(t.Context(), msgKey(roomA, mainThread, 1), "alice"); ok || err != nil {
		t.Fatalf("GetBookmark after refused writes = %+v, %v, %v; want nothing", got, ok, err)
	}
}
