package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) GetEditHistory(ctx context.Context, req *chatimv1.GetEditHistoryRequest) (*chatimv1.GetEditHistoryResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	key := store.MsgKey{Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq()}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	if err := domain.ValidateThread(key.Thread); err != nil {
		return nil, err
	}
	limit, err := domain.PageLimit(int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	rows, err := s.reader.EditHistory(ctx, who.Tenant, who.User, key, req.GetAfterVer(), limit)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &chatimv1.GetEditHistoryResponse{}, nil
	}
	return &chatimv1.GetEditHistoryResponse{Versions: pbconv.MessageVersions(rows)}, nil
}
