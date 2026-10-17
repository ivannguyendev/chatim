package grpcsrv

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomlist"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const defaultRoomListLimit = 20

var (
	errRoomListLimit = fmt.Errorf("%w: limit", apperr.ErrInvalidArgument)
	errRoomIDCount   = fmt.Errorf("%w: room ids", apperr.ErrInvalidArgument)
)

type RoomLister interface {
	List(ctx context.Context, q roomlist.Query) (roomlist.Page, error)
	Get(ctx context.Context, tenant, user string, rooms []uint64) ([]domain.RoomItem, []uint64, error)
}

func (s *Service) ListMyRooms(ctx context.Context, req *chatimv1.ListMyRoomsRequest) (*chatimv1.ListMyRoomsResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	folder, err := pbconv.DomainRoomFolder(req.GetFolder())
	if err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	switch {
	case limit == 0:
		limit = defaultRoomListLimit
	case limit > roomlist.MaxRooms:
		return nil, errRoomListLimit
	}
	q := roomlist.Query{Tenant: who.tenant, User: who.user, Folder: folder, Limit: limit}
	if next := req.GetNext(); next != "" {
		after, err := domain.DecodeRoomCursor(next, folder)
		if err != nil {
			return nil, err
		}
		q.After = &after
	}
	page, err := s.roomList.List(ctx, q)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListMyRoomsResponse{Items: roomItems(page.Items)}
	if page.Next != nil {
		resp.Next = domain.EncodeRoomCursor(folder, *page.Next)
	}
	return resp, nil
}

func (s *Service) GetRooms(ctx context.Context, req *chatimv1.GetRoomsRequest) (*chatimv1.GetRoomsResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.GetRoomIds()) > roomlist.MaxRooms {
		return nil, errRoomIDCount
	}
	rooms := make([]uint64, len(req.GetRoomIds()))
	for i, id := range req.GetRoomIds() {
		if rooms[i], err = parseRoomID(id); err != nil {
			return nil, err
		}
	}
	items, missing, err := s.roomList.Get(ctx, who.tenant, who.user, rooms)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.GetRoomsResponse{Items: roomItems(items), Missing: make([]string, len(missing))}
	for i, id := range missing {
		resp.Missing[i] = pbconv.RoomID(id)
	}
	return resp, nil
}

func roomItems(items []domain.RoomItem) []*chatimv1.RoomItem {
	out := make([]*chatimv1.RoomItem, len(items))
	for i, it := range items {
		out[i] = pbconv.RoomItem(it)
	}
	return out
}
