package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func bookmarkListCases() []interactionCase {
	return []interactionCase{
		{"bookmarks list live bookmarks of one user newest first with a cursor stable on ties", bookmarkList},
		{"invalid bookmark queries are rejected", bookmarkListInvalid},
	}
}

func bookmarkList(t *testing.T, s interactionStores) {
	set := func(seq uint64, user string, on bool, after time.Duration) domain.Bookmark {
		return mustSetBookmark(t, s.interactions, bookmarkAt(roomA, mainThread, seq, user, on, after), true)
	}
	b1, b2, b3, b4 := set(1, "alice", true, time.Second), set(2, "alice", true, 2*time.Second), set(3, "alice", true, 2*time.Second), set(4, "alice", true, 3*time.Second)
	set(5, "alice", true, 0)
	set(5, "alice", false, 4*time.Second)
	bob := set(1, "bob", true, 5*time.Second)
	sideB := mustSetBookmark(t, s.interactions, bookmarkAt(roomB, sideThread, 1, "alice", true, 2*time.Second), true)
	list := func(user string, before store.BookmarkCursor, limit int) ([]domain.Bookmark, error) {
		return s.interactions.Bookmarks(t.Context(), tenant, user, before, limit)
	}
	cursorOf := func(b domain.Bookmark) store.BookmarkCursor {
		return store.BookmarkCursor{At: b.At, Key: store.BookmarkKeyOf(b)}
	}
	got, err := list("alice", store.BookmarkCursor{}, 10)
	assertBookmarks(t, "Bookmarks(alice)", got, err, b4, sideB, b3, b2, b1)
	got, err = list("alice", store.BookmarkCursor{}, 2)
	assertBookmarks(t, "Bookmarks(alice, 2)", got, err, b4, sideB)
	got, err = list("alice", cursorOf(sideB), 2)
	assertBookmarks(t, "Bookmarks(alice, after sideB)", got, err, b3, b2)
	got, err = list("alice", cursorOf(b2), 2)
	assertBookmarks(t, "Bookmarks(alice, after b2)", got, err, b1)
	got, err = list("bob", store.BookmarkCursor{}, 10)
	assertBookmarks(t, "Bookmarks(bob)", got, err, bob)
	got, err = s.interactions.Bookmarks(t.Context(), "other", "alice", store.BookmarkCursor{}, 10)
	assertBookmarks(t, "Bookmarks(other tenant)", got, err)
}

func bookmarkListInvalid(t *testing.T, s interactionStores) {
	for name, c := range map[string]struct {
		tenant, user string
		limit        int
	}{
		"empty tenant":  {"", "alice", 10},
		"bad user":      {tenant, "a.b", 10},
		"zero limit":    {tenant, "alice", 0},
		"limit too big": {tenant, "alice", store.MaxPageLimit + 1},
	} {
		_, err := s.interactions.Bookmarks(t.Context(), c.tenant, c.user, store.BookmarkCursor{}, c.limit)
		assertErrorIs(t, "Bookmarks("+name+")", err, apperr.ErrInvalidArgument)
	}
}
