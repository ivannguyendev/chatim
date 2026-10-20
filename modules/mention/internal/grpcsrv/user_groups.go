package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) SetUserGroups(ctx context.Context, req *chatimv1.SetUserGroupsRequest) (*chatimv1.SetUserGroupsResponse, error) {
	who, err := callerid.Of(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.GetItems()) > MaxSetItems {
		return nil, invalid("too many user groups items")
	}
	items := make([]domain.UserGroups, len(req.GetItems()))
	for i, it := range req.GetItems() {
		items[i] = domain.UserGroups{Tenant: who.Tenant, User: it.GetUser(), Groups: it.GetGroups(), Ver: it.GetVer(), UpdatedBy: who.User}
	}
	if err := store.ValidateUserGroups(items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return &chatimv1.SetUserGroupsResponse{}, nil
	}
	changed, err := s.userGroups.Set(ctx, items)
	if err != nil {
		return nil, dependencyErr("set user groups", err)
	}
	return &chatimv1.SetUserGroupsResponse{ChangedUsers: changed}, nil
}
