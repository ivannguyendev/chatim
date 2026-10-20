package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/room"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) CreateRoom(ctx context.Context, req *chatimv1.CreateRoomRequest) (*chatimv1.CreateRoomResponse, error) {
	who, err := callerid.Of(ctx)
	if err != nil {
		return nil, err
	}
	typ, err := pbconv.DomainRoomType(req.GetType())
	if err != nil {
		return nil, err
	}
	got, err := s.roomService.CreateRoom(ctx, room.CreateRoomCmd{
		Tenant: who.Tenant, User: who.User, Type: typ, Name: req.GetName(), Members: req.GetMembers(), RequestID: req.GetRequestId(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.CreateRoomResponse{Room: pbconv.Room(got)}, nil
}
