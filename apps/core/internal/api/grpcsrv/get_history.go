package grpcsrv

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/view"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
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
	grant, err := s.access.Authorize(ctx, access.ReadHistory, who.Tenant, who.User, q.Room)
	if err != nil {
		return nil, err
	}
	page, err := s.historyPage(ctx, q)
	if err != nil {
		return nil, err
	}
	viewer, err := s.viewerOf(ctx, who.User, grant, q.PageQuery, page, quotedSeqs(q.Room, page)...)
	if err != nil {
		return nil, err
	}
	out, err := s.readerMessages(ctx, viewer, q.Room, s.view.Apply(viewer, page))
	if err != nil {
		return nil, err
	}
	return &chatimv1.GetHistoryResponse{Messages: out}, nil
}

func pageQueryOf(req *chatimv1.GetHistoryRequest) (historyQuery, error) {
	room, err := parseRoomID(req.GetRoomId())
	if err != nil {
		return historyQuery{}, err
	}
	if err := domain.ValidateThread(req.GetThreadRoot()); err != nil {
		return historyQuery{}, err
	}
	anchor, seq, err := anchorOf(req.GetAnchor(), req.GetSeq())
	if err != nil {
		return historyQuery{}, err
	}
	limit, err := domain.PageLimit(int(req.GetLimit()))
	if err != nil {
		return historyQuery{}, err
	}
	q := store.PageQuery{Room: room, Thread: req.GetThreadRoot(), Anchor: anchor, Seq: seq, Limit: limit}
	return historyQuery{PageQuery: q, around: req.GetAnchor() == chatimv1.HistoryAnchor_HISTORY_ANCHOR_AROUND}, nil
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

func (s *Service) viewerOf(ctx context.Context, user string, grant access.Request, q store.PageQuery, page []domain.Message, quoted ...uint64) (view.Viewer, error) {
	v := view.Viewer{User: user, Room: grant.Room, ClearedAt: grant.Member.ClearedAt}
	if len(page) == 0 {
		return v, nil
	}
	lo, hi, newest := page[0].Seq, page[0].Seq, page[0].CreatedAt
	for _, m := range page[1:] {
		lo, hi = min(lo, m.Seq), max(hi, m.Seq)
		if m.CreatedAt.After(newest) {
			newest = m.CreatedAt
		}
	}
	for _, seq := range quoted {
		lo = min(lo, seq)
	}
	if v.Cleared(newest) {
		return v, nil
	}
	seqs, err := s.hidden.HiddenIn(ctx, user, q.Room, q.Thread, lo, hi)
	if err != nil {
		return view.Viewer{}, err
	}
	v.HiddenSeqs = make(map[uint64]bool, len(seqs))
	for _, seq := range seqs {
		v.HiddenSeqs[seq] = true
	}
	return v, nil
}
