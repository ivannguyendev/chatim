package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) AddMembers(ctx context.Context, req *chatimv1.AddMembersRequest) (*chatimv1.AddMembersResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.AddMembersResponse, error) {
		return api.AddMembers(ctx, req)
	})
}

func (c *Client) RemoveMember(ctx context.Context, req *chatimv1.RemoveMemberRequest) (*chatimv1.RemoveMemberResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.RemoveMemberResponse, error) {
		return api.RemoveMember(ctx, req)
	})
}

func (c *Client) LeaveRoom(ctx context.Context, req *chatimv1.LeaveRoomRequest) (*chatimv1.LeaveRoomResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.LeaveRoomResponse, error) {
		return api.LeaveRoom(ctx, req)
	})
}

func (c *Client) ChangeMemberRole(ctx context.Context, req *chatimv1.ChangeMemberRoleRequest) (*chatimv1.ChangeMemberRoleResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ChangeMemberRoleResponse, error) {
		return api.ChangeMemberRole(ctx, req)
	})
}

func (c *Client) SetMemberPriority(ctx context.Context, req *chatimv1.SetMemberPriorityRequest) (*chatimv1.SetMemberPriorityResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.SetMemberPriorityResponse, error) {
		return api.SetMemberPriority(ctx, req)
	})
}

func (c *Client) MarkRead(ctx context.Context, req *chatimv1.MarkReadRequest) (*chatimv1.MarkReadResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.MarkReadResponse, error) {
		return api.MarkRead(ctx, req)
	})
}

func (c *Client) MarkUnread(ctx context.Context, req *chatimv1.MarkUnreadRequest) (*chatimv1.MarkUnreadResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.MarkUnreadResponse, error) {
		return api.MarkUnread(ctx, req)
	})
}
