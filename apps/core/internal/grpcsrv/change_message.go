package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) EditMessage(ctx context.Context, req *chatimv1.EditMessageRequest) (*chatimv1.EditMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	m, err := s.mutator.Edit(ctx, mutate.EditCmd{
		Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq(),
		BaseVersion: req.GetBaseVer(), Text: req.GetText(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.EditMessageResponse{Message: pbconv.Message(m)}, nil
}

func (s *Service) DeleteMessage(ctx context.Context, req *chatimv1.DeleteMessageRequest) (*chatimv1.DeleteMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	m, err := s.mutator.Delete(ctx, mutate.DeleteCmd{
		Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq(), BaseVersion: req.GetBaseVer(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.DeleteMessageResponse{Message: pbconv.Message(m)}, nil
}

func (s *Service) HideMessage(ctx context.Context, req *chatimv1.HideMessageRequest) (*chatimv1.HideMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	err = s.mutator.Hide(ctx, mutate.HideCmd{Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.HideMessageResponse{}, nil
}

func (s *Service) ClearHistory(ctx context.Context, req *chatimv1.ClearHistoryRequest) (*chatimv1.ClearHistoryResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	n, err := s.mutator.ClearHistory(ctx, mutate.ClearCmd{Tenant: who.tenant, User: who.user, Room: room, UpToSeq: req.GetUpToSeq()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.ClearHistoryResponse{ClearedBeforeSeq: n}, nil
}

func callerAndRoom(ctx context.Context, roomID string) (caller, uint64, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return caller{}, 0, err
	}
	room, err := parseRoomID(roomID)
	if err != nil {
		return caller{}, 0, err
	}
	return who, room, nil
}
