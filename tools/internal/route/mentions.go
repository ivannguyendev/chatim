package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

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
	return call(ctx, c, c.loc.AnyAddr, retryIdempotent, func(ctx context.Context, api chatimv1.CoreServiceClient) (T, error) {
		mentions, ok := api.(chatimv1.MentionServiceClient)
		if !ok {
			var zero T
			return zero, errNoMentionService
		}
		return do(ctx, mentions)
	})
}
