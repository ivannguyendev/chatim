package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

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
	page, err := s.interactionReader.Bookmarks(ctx, who.Tenant, who.User, before, int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListBookmarksResponse{Items: make([]*chatimv1.BookmarkItem, len(page.Items))}
	for i, it := range page.Items {
		resp.Items[i] = &chatimv1.BookmarkItem{Message: pbconv.Message(it.Message), Available: it.Available}
	}
	if page.Next != nil {
		resp.Next = encodeBookmarkCursor(*page.Next)
	}
	return resp, nil
}
