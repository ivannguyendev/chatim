package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/room"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) OpenDirectRoom(ctx context.Context, req *chatimv1.OpenDirectRoomRequest) (*chatimv1.OpenDirectRoomResponse, error) {
	who, err := callerid.Of(ctx)
	if err != nil {
		return nil, err
	}
	got, created, err := s.roomService.OpenDirectRoom(ctx, room.OpenDirectCmd{Tenant: who.Tenant, User: who.User, Other: req.GetOtherUser()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.OpenDirectRoomResponse{Room: pbconv.Room(got), Created: created}, nil
}
