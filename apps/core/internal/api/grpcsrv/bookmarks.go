package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type BookmarkLister interface {
	Bookmarks(ctx context.Context, tenant, user string, before store.BookmarkCursor, limit int) ([]domain.Bookmark, error)
}

func (s *Service) SetBookmark(ctx context.Context, req *chatimv1.SetBookmarkRequest) (*chatimv1.SetBookmarkResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	changed, err := s.interactionService.SetBookmark(ctx, interaction.BookmarkCmd{
		Tenant: who.Tenant, User: who.User, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq(), On: req.GetOn(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.SetBookmarkResponse{Changed: changed}, nil
}

func (s *Service) ListBookmarks(ctx context.Context, req *chatimv1.ListBookmarksRequest) (*chatimv1.ListBookmarksResponse, error) {
	who, err := callerid.Of(ctx)
	if err != nil {
		return nil, err
	}
	before, err := decodeBookmarkCursor(req.GetBefore())
	if err != nil {
		return nil, err
	}
	limit, err := domain.PageLimit(int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	marks, err := s.bookmarks.Bookmarks(ctx, who.Tenant, who.User, before, limit)
	if err != nil {
		return nil, err
	}
	visible, err := s.bookmarkedMessages(ctx, who, marks)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListBookmarksResponse{Items: make([]*chatimv1.BookmarkItem, len(marks))}
	for i, b := range marks {
		key := store.BookmarkKeyOf(b)
		m, ok := visible[key]
		if !ok {
			m = domain.Message{Room: key.Room, Thread: key.Thread, Seq: key.Seq}
		}
		resp.Items[i] = &chatimv1.BookmarkItem{Message: pbconv.Message(m), Available: ok}
	}
	if len(marks) == limit {
		resp.Next = encodeBookmarkCursor(marks[len(marks)-1])
	}
	return resp, nil
}

func (s *Service) bookmarkedMessages(ctx context.Context, who callerid.Caller, marks []domain.Bookmark) (map[store.MsgKey]domain.Message, error) {
	keys := make([]store.MsgKey, len(marks))
	for i, b := range marks {
		keys[i] = store.BookmarkKeyOf(b)
	}
	return s.reader.VisibleMessages(ctx, who.Tenant, who.User, keys)
}
