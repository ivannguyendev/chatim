package api

import (
	"context"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
)

func (a adminService) CreateGroup(ctx context.Context, req *connect.Request[iamv1.CreateGroupRequest]) (*connect.Response[iamv1.CreateGroupResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.CreateGroupRequest) (*iamv1.CreateGroupResponse, error) {
		in := manage.NewGroup{ID: msg.GetId(), Name: msg.GetName(), ParentID: msg.GetParentId(), Mentionable: msg.Mentionable}
		g, err := a.s.opts.Admin.CreateGroup(ctx, c, in)
		if err != nil {
			return nil, err
		}
		return &iamv1.CreateGroupResponse{Group: adminGroup(g, nil)}, nil
	})
}

func (a adminService) UpdateGroup(ctx context.Context, req *connect.Request[iamv1.UpdateGroupRequest]) (*connect.Response[iamv1.UpdateGroupResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.UpdateGroupRequest) (*iamv1.UpdateGroupResponse, error) {
		g, err := a.s.opts.Admin.UpdateGroup(ctx, c, msg.GetId(), groupUpdate(msg))
		if err != nil {
			return nil, err
		}
		return &iamv1.UpdateGroupResponse{Group: adminGroup(g, nil)}, nil
	})
}

func (a adminService) DeleteGroup(ctx context.Context, req *connect.Request[iamv1.DeleteGroupRequest]) (*connect.Response[iamv1.DeleteGroupResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.DeleteGroupRequest) (*iamv1.DeleteGroupResponse, error) {
		return &iamv1.DeleteGroupResponse{}, a.s.opts.Admin.DeleteGroup(ctx, c, msg.GetGroupId())
	})
}

func (a adminService) ListGroups(ctx context.Context, req *connect.Request[iamv1.ListGroupsRequest]) (*connect.Response[iamv1.ListGroupsResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.ListGroupsRequest) (*iamv1.ListGroupsResponse, error) {
		page, next, err := a.s.opts.Admin.ListGroups(ctx, c, msg.GetPageToken())
		if err != nil {
			return nil, err
		}
		return &iamv1.ListGroupsResponse{Groups: adminGroups(page), NextPageToken: next}, nil
	})
}

func (a adminService) SetAccountGroups(ctx context.Context, req *connect.Request[iamv1.SetAccountGroupsRequest]) (*connect.Response[iamv1.SetAccountGroupsResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.SetAccountGroupsRequest) (*iamv1.SetAccountGroupsResponse, error) {
		return &iamv1.SetAccountGroupsResponse{}, a.s.opts.Admin.SetAccountGroups(ctx, c, msg.GetAccountId(), msg.GetGroupIds())
	})
}

func (a adminService) GetAccount(ctx context.Context, req *connect.Request[iamv1.GetAccountRequest]) (*connect.Response[iamv1.GetAccountResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.GetAccountRequest) (*iamv1.GetAccountResponse, error) {
		view, err := a.s.opts.Admin.GetAccount(ctx, c, msg.GetAccountId())
		if err != nil {
			return nil, err
		}
		return &iamv1.GetAccountResponse{Account: adminAccount(view)}, nil
	})
}

func (a adminService) ListAccounts(ctx context.Context, req *connect.Request[iamv1.ListAccountsRequest]) (*connect.Response[iamv1.ListAccountsResponse], error) {
	return serveAdmin(ctx, a.s, req, func(ctx context.Context, c auth.Caller, msg *iamv1.ListAccountsRequest) (*iamv1.ListAccountsResponse, error) {
		page, next, err := a.s.opts.Admin.ListAccounts(ctx, c, msg.GetPageToken())
		if err != nil {
			return nil, err
		}
		return &iamv1.ListAccountsResponse{Accounts: adminAccounts(page), NextPageToken: next}, nil
	})
}
