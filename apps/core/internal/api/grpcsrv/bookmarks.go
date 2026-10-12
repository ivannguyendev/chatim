package grpcsrv

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type BookmarkLister interface {
	Bookmarks(ctx context.Context, tenant, user string, before store.BookmarkCursor, limit int) ([]domain.Bookmark, error)
}

type roomThread struct{ room, thread uint64 }

func (s *Service) SetBookmark(ctx context.Context, req *chatimv1.SetBookmarkRequest) (*chatimv1.SetBookmarkResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	changed, err := s.mutator.SetBookmark(ctx, mutate.BookmarkCmd{
		Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq(), On: req.GetOn(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.SetBookmarkResponse{Changed: changed}, nil
}

func (s *Service) ListBookmarks(ctx context.Context, req *chatimv1.ListBookmarksRequest) (*chatimv1.ListBookmarksResponse, error) {
	who, err := callerOf(ctx)
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
	marks, err := s.bookmarks.Bookmarks(ctx, who.tenant, who.user, before, limit)
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

func (s *Service) bookmarkedMessages(ctx context.Context, who caller, marks []domain.Bookmark) (map[store.MsgKey]domain.Message, error) {
	keys := make([]store.MsgKey, len(marks))
	for i, b := range marks {
		keys[i] = store.BookmarkKeyOf(b)
	}
	return s.visibleMessages(ctx, who, keys)
}

func (s *Service) visibleMessages(ctx context.Context, who caller, keys []store.MsgKey) (map[store.MsgKey]domain.Message, error) {
	groups := make(map[roomThread][]store.MsgKey)
	var order []roomThread
	for _, k := range keys {
		g := roomThread{k.Room, k.Thread}
		if _, seen := groups[g]; !seen {
			order = append(order, g)
		}
		groups[g] = append(groups[g], k)
	}
	visible := make(map[store.MsgKey]domain.Message, len(keys))
	for _, g := range order {
		if err := s.visibleIn(ctx, who, g, groups[g], visible); err != nil {
			return nil, err
		}
	}
	return visible, nil
}

func (s *Service) visibleIn(ctx context.Context, who caller, g roomThread, keys []store.MsgKey, visible map[store.MsgKey]domain.Message) error {
	grant, err := s.access.Authorize(ctx, access.ReadHistory, who.tenant, who.user, g.room)
	if errors.Is(err, apperr.ErrPermissionDenied) || errors.Is(err, apperr.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	found, err := s.pages.Find(ctx, g.room, keys)
	if err != nil {
		return err
	}
	viewer, err := s.viewerOf(ctx, who.user, grant, store.PageQuery{Room: g.room, Thread: g.thread}, found)
	if err != nil {
		return err
	}
	for _, m := range s.bookmarkView.Apply(viewer, found) {
		if !m.Deleted && !m.Hidden {
			visible[store.KeyOf(m)] = m
		}
	}
	return nil
}
