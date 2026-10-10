package effects

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const BookmarkEventName = "bookmark_event"

var errNoBookmark = fmt.Errorf("bookmark %w", apperr.ErrNotFound)

type BookmarkEventDeps struct {
	Bookmarks BookmarkReader
	Rooms     RoomReader
	JS        publish.JetStream
}

type BookmarkEvent struct {
	eventPublisher
	bookmarks BookmarkReader
	delay     time.Duration
}

func NewBookmarkEvent(deps BookmarkEventDeps, cfg MessageChangedConfig) (*BookmarkEvent, error) {
	if deps.Bookmarks == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs bookmarks, rooms and a jetstream client", apperr.ErrInvalidArgument, BookmarkEventName)
	}
	cfg, err := eventConfig(BookmarkEventName, cfg)
	if err != nil {
		return nil, err
	}
	e := &BookmarkEvent{bookmarks: deps.Bookmarks, delay: cfg.Delay}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return e, nil
}

func (e *BookmarkEvent) Effect() Effect {
	return Effect{Name: BookmarkEventName, Delay: e.delay, Run: e.run}
}

func (e *BookmarkEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, bookmarkGone)
}

func bookmarkGone(err error) bool { return gone(err) || errors.Is(err, errNoBookmark) }

func (e *BookmarkEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	doc, found, err := e.bookmarks.GetBookmark(ctx, recordKey(r), r.User)
	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, errNoBookmark
	case doc.Ver > r.Version:
		return nil, nil
	case doc.Ver < r.Version:
		return nil, store.ErrStaleRead
	}
	room, err := e.types.identity(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.BookmarkChanged(room, doc), nil
}
