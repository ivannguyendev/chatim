package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) ListMyRoomIDs(ctx context.Context, _ *chatimv1.ListMyRoomIDsRequest) (*chatimv1.ListMyRoomIDsResponse, error) {
	who, err := callerid.Of(ctx)
	if err != nil {
		return nil, err
	}
	rooms, err := s.memberReader.MyRoomVersions(ctx, who.Tenant, who.User)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListMyRoomIDsResponse{Rooms: make([]*chatimv1.MyRoomID, 0, len(rooms))}
	for _, r := range rooms {
		id := &chatimv1.MyRoomID{RoomId: pbconv.RoomID(r.Room), Ver: r.Ver}
		if r.Left {
			resp.Left = append(resp.Left, id)
			continue
		}
		resp.Rooms = append(resp.Rooms, id)
	}
	return resp, nil
}
