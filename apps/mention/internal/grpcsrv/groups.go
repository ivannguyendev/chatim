package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) SetGroups(ctx context.Context, req *chatimv1.SetGroupsRequest) (*chatimv1.SetGroupsResponse, error) {
	who, err := callerid.Of(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.GetItems()) > MaxSetItems {
		return nil, invalid("too many group items")
	}
	items := make([]domain.Group, len(req.GetItems()))
	for i, it := range req.GetItems() {
		items[i] = domain.Group{Tenant: who.Tenant, ID: it.GetId(), Name: it.GetName(), Active: it.GetActive(), Ver: it.GetVer(), UpdatedBy: who.User}
	}
	if err := store.ValidateGroups(items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return &chatimv1.SetGroupsResponse{}, nil
	}
	changed, err := s.groups.Set(ctx, items)
	s.catalog.Forget(who.Tenant)
	if err != nil {
		return nil, dependencyErr("set groups", err)
	}
	return &chatimv1.SetGroupsResponse{ChangedGroups: changed}, nil
}
