package api

import (
	"context"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
)

func (a adminService) CreateRole(ctx context.Context, req *connect.Request[iamv1.CreateRoleRequest]) (*connect.Response[iamv1.CreateRoleResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.CreateRoleRequest) (*iamv1.CreateRoleResponse, error) {
		role, err := a.s.opts.Admin.CreateRole(ctx, c, manage.NewRole{ID: msg.GetId(), Name: msg.GetName(), Permissions: msg.GetPermissions()})
		if err != nil {
			return nil, err
		}
		return &iamv1.CreateRoleResponse{Role: adminRole(role)}, nil
	})
}

func (a adminService) UpdateRole(ctx context.Context, req *connect.Request[iamv1.UpdateRoleRequest]) (*connect.Response[iamv1.UpdateRoleResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.UpdateRoleRequest) (*iamv1.UpdateRoleResponse, error) {
		role, err := a.s.opts.Admin.UpdateRole(ctx, c, msg.GetId(), roleUpdate(msg))
		if err != nil {
			return nil, err
		}
		return &iamv1.UpdateRoleResponse{Role: adminRole(role)}, nil
	})
}

func (a adminService) DeleteRole(ctx context.Context, req *connect.Request[iamv1.DeleteRoleRequest]) (*connect.Response[iamv1.DeleteRoleResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.DeleteRoleRequest) (*iamv1.DeleteRoleResponse, error) {
		removed, err := a.s.opts.Admin.DeleteRole(ctx, c, msg.GetRoleId())
		if err != nil {
			return nil, err
		}
		return &iamv1.DeleteRoleResponse{AccountsUnassigned: int64(removed.Accounts), GroupsUnassigned: int64(removed.Groups)}, nil
	})
}

func (a adminService) GetRole(ctx context.Context, req *connect.Request[iamv1.GetRoleRequest]) (*connect.Response[iamv1.GetRoleResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.GetRoleRequest) (*iamv1.GetRoleResponse, error) {
		role, err := a.s.opts.Admin.GetRole(ctx, c, msg.GetRoleId())
		if err != nil {
			return nil, err
		}
		return &iamv1.GetRoleResponse{Role: adminRole(role)}, nil
	})
}

func (a adminService) ListRoles(ctx context.Context, req *connect.Request[iamv1.ListRolesRequest]) (*connect.Response[iamv1.ListRolesResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.ListRolesRequest) (*iamv1.ListRolesResponse, error) {
		page, next, err := a.s.opts.Admin.ListRoles(ctx, c, msg.GetPageToken())
		if err != nil {
			return nil, err
		}
		return &iamv1.ListRolesResponse{Roles: adminRoles(page), NextPageToken: next}, nil
	})
}

func (a adminService) AssignAccountRole(ctx context.Context, req *connect.Request[iamv1.AssignAccountRoleRequest]) (*connect.Response[iamv1.AssignAccountRoleResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.AssignAccountRoleRequest) (*iamv1.AssignAccountRoleResponse, error) {
		return &iamv1.AssignAccountRoleResponse{}, a.s.opts.Admin.AssignAccountRole(ctx, c, msg.GetAccountId(), msg.GetRoleId())
	})
}

func (a adminService) UnassignAccountRole(ctx context.Context, req *connect.Request[iamv1.UnassignAccountRoleRequest]) (*connect.Response[iamv1.UnassignAccountRoleResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.UnassignAccountRoleRequest) (*iamv1.UnassignAccountRoleResponse, error) {
		return &iamv1.UnassignAccountRoleResponse{}, a.s.opts.Admin.UnassignAccountRole(ctx, c, msg.GetAccountId(), msg.GetRoleId())
	})
}

func (a adminService) AssignGroupRole(ctx context.Context, req *connect.Request[iamv1.AssignGroupRoleRequest]) (*connect.Response[iamv1.AssignGroupRoleResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.AssignGroupRoleRequest) (*iamv1.AssignGroupRoleResponse, error) {
		return &iamv1.AssignGroupRoleResponse{}, a.s.opts.Admin.AssignGroupRole(ctx, c, msg.GetGroupId(), msg.GetRoleId())
	})
}

func (a adminService) UnassignGroupRole(ctx context.Context, req *connect.Request[iamv1.UnassignGroupRoleRequest]) (*connect.Response[iamv1.UnassignGroupRoleResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.UnassignGroupRoleRequest) (*iamv1.UnassignGroupRoleResponse, error) {
		return &iamv1.UnassignGroupRoleResponse{}, a.s.opts.Admin.UnassignGroupRole(ctx, c, msg.GetGroupId(), msg.GetRoleId())
	})
}
