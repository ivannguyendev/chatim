package interaction

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type BookmarkItem struct {
	Message   domain.Message
	Available bool
}

type BookmarkPage struct {
	Items []BookmarkItem
	Next  *domain.Bookmark
}

func (r *Reader) Bookmarks(ctx context.Context, tenant, user string, before store.BookmarkCursor, limit int) (BookmarkPage, error) {
	n, err := domain.PageLimit(limit)
	if err != nil {
		return BookmarkPage{}, err
	}
	marks, err := r.d.Interactions.Bookmarks(ctx, tenant, user, before, n)
	if err != nil {
		return BookmarkPage{}, err
	}
	keys := make([]store.MsgKey, len(marks))
	for i, b := range marks {
		keys[i] = store.BookmarkKeyOf(b)
	}
	visible, err := r.d.Views.VisibleMessages(ctx, tenant, user, keys)
	if err != nil {
		return BookmarkPage{}, err
	}
	page := BookmarkPage{Items: make([]BookmarkItem, len(marks))}
	for i, key := range keys {
		m, ok := visible[key]
		if !ok {
			m = domain.Message{Room: key.Room, Thread: key.Thread, Seq: key.Seq}
		}
		page.Items[i] = BookmarkItem{Message: m, Available: ok}
	}
	if len(marks) == n {
		page.Next = &marks[len(marks)-1]
	}
	return page, nil
}
