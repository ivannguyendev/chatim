package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) UpdateRoom(ctx context.Context, req *chatimv1.UpdateRoomRequest) (*chatimv1.UpdateRoomResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.UpdateRoom(ctx, mutate.UpdateRoomCmd{Tenant: who.tenant, User: who.user, Room: room, Update: pbconv.DomainRoomUpdate(req)})
	if err != nil {
		return nil, err
	}
	return &chatimv1.UpdateRoomResponse{Room: pbconv.Room(res.Room), Changed: res.Changed}, nil
}

func (s *Service) SetRoomPreferences(ctx context.Context, req *chatimv1.SetRoomPreferencesRequest) (*chatimv1.SetRoomPreferencesResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.SetRoomPreferences(ctx, mutate.PreferencesCmd{Tenant: who.tenant, User: who.user, Room: room, Change: pbconv.DomainPreferencesChange(req)})
	if err != nil {
		return nil, err
	}
	return &chatimv1.SetRoomPreferencesResponse{Changed: res.Changed, State: pbconv.MyRoomState(res.Member)}, nil
}
