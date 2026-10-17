package grpcsrv

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const maxMessageSeqs = 100

var errMessageSeqs = fmt.Errorf("%w: seqs", apperr.ErrInvalidArgument)

func (s *Service) GetMessages(ctx context.Context, req *chatimv1.GetMessagesRequest) (*chatimv1.GetMessagesResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateThread(req.GetThreadRoot()); err != nil {
		return nil, err
	}
	asked, err := messageKeysOf(room, req.GetSeqs())
	if err != nil {
		return nil, err
	}
	grant, err := s.access.Authorize(ctx, access.ReadHistory, who.Tenant, who.User, room)
	if err != nil {
		return nil, err
	}
	found, err := s.pages.Find(ctx, room, asked)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(found, func(a, b domain.Message) int { return cmp.Compare(a.Seq, b.Seq) })
	viewer, err := s.viewerOf(ctx, who.User, grant, store.PageQuery{Room: room}, found, quotedSeqs(room, found)...)
	if err != nil {
		return nil, err
	}
	out, err := s.readerMessages(ctx, viewer, room, s.view.Apply(viewer, found))
	if err != nil {
		return nil, err
	}
	return &chatimv1.GetMessagesResponse{Messages: out}, nil
}

func messageKeysOf(room uint64, seqs []uint64) ([]store.MsgKey, error) {
	if len(seqs) == 0 || len(seqs) > maxMessageSeqs {
		return nil, errMessageSeqs
	}
	sorted := slices.Compact(slices.Sorted(slices.Values(seqs)))
	if sorted[0] == 0 || sorted[len(sorted)-1] == math.MaxUint64 {
		return nil, errMessageSeqs
	}
	out := make([]store.MsgKey, len(sorted))
	for i, seq := range sorted {
		out[i] = store.MsgKey{Room: room, Seq: seq}
	}
	return out, nil
}
