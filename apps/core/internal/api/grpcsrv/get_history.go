package grpcsrv

import (
	"context"
	"fmt"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errUnknownAnchor = fmt.Errorf("%w: anchor", apperr.ErrInvalidArgument)
	errAnchorSeq     = fmt.Errorf("%w: seq", apperr.ErrInvalidArgument)
)

func (s *Service) GetHistory(ctx context.Context, req *chatimv1.GetHistoryRequest) (*chatimv1.GetHistoryResponse, error) {
	who, err := callerid.Of(ctx)
	if err != nil {
		return nil, err
	}
	q, err := pageQueryOf(req)
	if err != nil {
		return nil, err
	}
	items, err := s.reader.History(ctx, who.Tenant, who.User, q)
	if err != nil {
		return nil, err
	}
	return &chatimv1.GetHistoryResponse{Messages: messagesOf(items)}, nil
}

func pageQueryOf(req *chatimv1.GetHistoryRequest) (message.HistoryQuery, error) {
	room, err := parseRoomID(req.GetRoomId())
	if err != nil {
		return message.HistoryQuery{}, err
	}
	if err := domain.ValidateThread(req.GetThreadRoot()); err != nil {
		return message.HistoryQuery{}, err
	}
	anchor, seq, err := anchorOf(req.GetAnchor(), req.GetSeq())
	if err != nil {
		return message.HistoryQuery{}, err
	}
	limit, err := domain.PageLimit(int(req.GetLimit()))
	if err != nil {
		return message.HistoryQuery{}, err
	}
	q := store.PageQuery{Room: room, Thread: req.GetThreadRoot(), Anchor: anchor, Seq: seq, Limit: limit}
	return message.HistoryQuery{PageQuery: q, Around: req.GetAnchor() == chatimv1.HistoryAnchor_HISTORY_ANCHOR_AROUND}, nil
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
	case chatimv1.HistoryAnchor_HISTORY_ANCHOR_AROUND:
		return aroundAnchor(seq)
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

func aroundAnchor(seq uint64) (store.Anchor, uint64, error) {
	if seq == math.MaxUint64 {
		return 0, 0, errAnchorSeq
	}
	return seqAnchor(store.Before, seq)
}
