package route

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/coreroute"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errNoMentionService = status.Error(codes.Unimplemented, "route: the dialed core client has no mention service")

func (c *Client) ListMentions(ctx context.Context, req *chatimv1.ListMentionsRequest) (*chatimv1.ListMentionsResponse, Stats, error) {
	return viaMention(ctx, c, func(ctx context.Context, api chatimv1.MentionServiceClient) (*chatimv1.ListMentionsResponse, error) {
		return api.ListMentions(ctx, req)
	})
}

func (c *Client) SetUserGroups(ctx context.Context, req *chatimv1.SetUserGroupsRequest) (*chatimv1.SetUserGroupsResponse, Stats, error) {
	return viaMention(ctx, c, func(ctx context.Context, api chatimv1.MentionServiceClient) (*chatimv1.SetUserGroupsResponse, error) {
		return api.SetUserGroups(ctx, req)
	})
}

func (c *Client) SetGroups(ctx context.Context, req *chatimv1.SetGroupsRequest) (*chatimv1.SetGroupsResponse, Stats, error) {
	return viaMention(ctx, c, func(ctx context.Context, api chatimv1.MentionServiceClient) (*chatimv1.SetGroupsResponse, error) {
		return api.SetGroups(ctx, req)
	})
}

func (c *Client) ListRoomGroups(ctx context.Context, req *chatimv1.ListRoomGroupsRequest) (*chatimv1.ListRoomGroupsResponse, Stats, error) {
	return viaMention(ctx, c, func(ctx context.Context, api chatimv1.MentionServiceClient) (*chatimv1.ListRoomGroupsResponse, error) {
		return api.ListRoomGroups(ctx, req)
	})
}

func viaMention[T any](ctx context.Context, c *Client, do func(context.Context, chatimv1.MentionServiceClient) (T, error)) (T, Stats, error) {
	return call(ctx, c, c.routes.Any, func(ctx context.Context, conn coreroute.Conn) (T, error) {
		if conn.Mention == nil {
			var zero T
			return zero, errNoMentionService
		}
		return do(ctx, conn.Mention)
	})
}
