package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
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
	grant, err := s.access.Admit(ctx, access.ReadEditHistory, who.tenant, who.user, room)
	if err != nil {
		return nil, err
	}
	found, err := s.pages.Find(ctx, room, []store.MsgKey{key})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, domain.ErrMessageNotFound
	}
	grant.Author = found[0].From
	if err := s.access.Allow(ctx, grant); err != nil {
		return nil, err
	}
	if found[0].Deleted {
		return &chatimv1.GetEditHistoryResponse{}, nil
	}
	edits, err := s.edits.History(ctx, key, req.GetAfterVersion(), limit)
	if err != nil {
		return nil, err
	}
	return &chatimv1.GetEditHistoryResponse{Versions: pbconv.MessageVersions(found[0], edits, req.GetAfterVersion())}, nil
}
