package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) ListRoomGroups(ctx context.Context, req *chatimv1.ListRoomGroupsRequest) (*chatimv1.ListRoomGroupsResponse, error) {
	who, err := callerid.Of(ctx)
	if err != nil {
		return nil, err
	}
	room, err := ids.ParseRoomID(req.GetRoomId())
	if err != nil {
		return nil, invalid("room id")
	}
	if err := s.core.Authorize(ctx, who.Tenant, who.User, room, domain.ActionReadMembers); err != nil {
		return nil, dependencyErr("authorize read members", err)
	}
	members, err := s.core.RoomMemberCount(ctx, room)
	if err != nil {
		return nil, dependencyErr("room member count", err)
	}
	set, err := s.roomGroups.Of(ctx, who.Tenant, room, members)
	if err != nil {
		return nil, dependencyErr("room groups", err)
	}
	active, err := s.catalog.Active(ctx, who.Tenant)
	if err != nil {
		return nil, dependencyErr("group catalog", err)
	}
	resp := &chatimv1.ListRoomGroupsResponse{Filtered: set.Filtered}
	for _, g := range active {
		if _, in := set.Groups[g.ID]; in || !set.Filtered {
			resp.Groups = append(resp.Groups, &chatimv1.RoomGroup{Id: g.ID, Name: g.Name})
		}
	}
	return resp, nil
}
