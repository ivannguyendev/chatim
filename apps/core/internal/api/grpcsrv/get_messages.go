package grpcsrv

import (
	"context"
	"fmt"
	"math"
	"slices"

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
	items, err := s.reader.Messages(ctx, who.Tenant, who.User, room, asked)
	if err != nil {
		return nil, err
	}
	return &chatimv1.GetMessagesResponse{Messages: messagesOf(items)}, nil
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
