package grpcsrv

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/view"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errUnknownAnchor = fmt.Errorf("%w: anchor", apperr.ErrInvalidArgument)
	errAnchorSeq     = fmt.Errorf("%w: seq", apperr.ErrInvalidArgument)
)

func (s *Service) GetHistory(ctx context.Context, req *chatimv1.GetHistoryRequest) (*chatimv1.GetHistoryResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := pageQueryOf(req)
	if err != nil {
		return nil, err
	}
	grant, err := s.access.Authorize(ctx, access.ReadHistory, who.tenant, who.user, q.Room)
	if err != nil {
		return nil, err
	}
	page, err := s.pages.Page(ctx, q)
	if err != nil {
		return nil, err
	}
	page = s.view.Apply(view.Viewer{User: who.user, Room: grant.Room}, page)
	out := make([]*chatimv1.Message, len(page))
	for i, m := range page {
		out[i] = pbconv.Message(m)
	}
	return &chatimv1.GetHistoryResponse{Messages: out}, nil
}

func pageQueryOf(req *chatimv1.GetHistoryRequest) (store.PageQuery, error) {
	room, err := parseRoomID(req.GetRoomId())
	if err != nil {
		return store.PageQuery{}, err
	}
	if err := domain.ValidateThread(req.GetThreadRoot()); err != nil {
		return store.PageQuery{}, err
	}
	anchor, seq, err := anchorOf(req.GetAnchor(), req.GetSeq())
	if err != nil {
		return store.PageQuery{}, err
	}
	limit, err := domain.PageLimit(int(req.GetLimit()))
	if err != nil {
		return store.PageQuery{}, err
	}
	return store.PageQuery{Room: room, Thread: req.GetThreadRoot(), Anchor: anchor, Seq: seq, Limit: limit}, nil
}

func anchorOf(a chatimv1.HistoryAnchor, seq uint64) (store.Anchor, uint64, error) {
	switch a {
	case chatimv1.HistoryAnchor_HISTORY_ANCHOR_UNSPECIFIED, chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST:
		return store.Latest, 0, nil
	case chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST:
		return store.Oldest, 0, nil
	case chatimv1.HistoryAnchor_HISTORY_ANCHOR_BEFORE:
		return seqAnchor(store.Before, seq)
	case chatimv1.HistoryAnchor_HISTORY_ANCHOR_AFTER:
		return seqAnchor(store.After, seq)
	default:
		return 0, 0, errUnknownAnchor
	}
}

func seqAnchor(a store.Anchor, seq uint64) (store.Anchor, uint64, error) {
	if seq == 0 {
		return 0, 0, errAnchorSeq
	}
	return a, seq, nil
}
