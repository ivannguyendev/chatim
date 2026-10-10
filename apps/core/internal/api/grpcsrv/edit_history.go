package grpcsrv

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
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
	rows, err := s.edits.History(ctx, key, req.GetAfterVer(), limit)
	if err != nil || len(rows) == 0 {
		return &chatimv1.GetEditHistoryResponse{}, err
	}
	switch deleting, err := s.deleting(ctx, key, rows, limit); {
	case err != nil:
		return nil, err
	case deleting:
		return &chatimv1.GetEditHistoryResponse{}, nil
	}
	if req.GetAfterVer() == 0 {
		switch row, err := s.edits.At(ctx, key, 0); {
		case err == nil:
			rows = append([]domain.Edit{row}, rows...)
		case !errors.Is(err, store.ErrEditNotFound):
			return nil, err
		}
	}
	return &chatimv1.GetEditHistoryResponse{Versions: pbconv.MessageVersions(rows)}, nil
}

func (s *Service) deleting(ctx context.Context, key store.MsgKey, page []domain.Edit, limit int) (bool, error) {
	last := page[len(page)-1]
	if len(page) == limit {
		latest, ok, err := s.edits.Latest(ctx, key)
		if err != nil || !ok {
			return false, err
		}
		last = latest
	}
	return last.Kind == domain.EditDelete, nil
}
