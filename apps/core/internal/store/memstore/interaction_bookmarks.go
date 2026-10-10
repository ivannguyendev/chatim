package memstore

import (
	"bytes"
	"cmp"
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Interactions) SetBookmark(ctx context.Context, b domain.Bookmark) (domain.Bookmark, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Bookmark{}, false, err
	}
	if err := store.ValidateBookmark(b); err != nil {
		return domain.Bookmark{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := userKey{key: store.BookmarkKeyOf(b), user: b.User}
	cur := s.bookmarks[k]
	if cur.On == b.On {
		return cur, false, nil
	}
	next := b
	next.Ver = cur.Ver + 1
	s.bookmarks[k] = next
	if s.log != nil {
		s.log.appendFact(logged{kind: store.BookmarkChanged, bookmark: next})
	}
	return next, true, nil
}

func (s *Interactions) GetBookmark(ctx context.Context, key store.MsgKey, user string) (domain.Bookmark, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Bookmark{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cur, ok := s.bookmarks[userKey{key: key, user: user}]
	return cur, ok, nil
}

func (s *Interactions) Bookmarks(ctx context.Context, tenant, user string, before store.BookmarkCursor, limit int) ([]domain.Bookmark, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateBookmarkQuery(tenant, user, limit); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Bookmark{}
	for k, b := range s.bookmarks {
		if k.user == user && b.Tenant == tenant && b.On && olderThan(b, before) {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, newestFirst)
	return out[:min(len(out), limit)], nil
}

func olderThan(b domain.Bookmark, before store.BookmarkCursor) bool {
	if before.At.IsZero() {
		return true
	}
	return cmp.Or(b.At.Compare(before.At), bytes.Compare(bookmarkMsgKey(store.BookmarkKeyOf(b)), bookmarkMsgKey(before.Key))) < 0
}

func newestFirst(a, b domain.Bookmark) int {
	return cmp.Or(b.At.Compare(a.At), bytes.Compare(bookmarkMsgKey(store.BookmarkKeyOf(b)), bookmarkMsgKey(store.BookmarkKeyOf(a))))
}

func bookmarkMsgKey(k store.MsgKey) []byte { return keys.Msg(k.Room, k.Thread, k.Seq) }
