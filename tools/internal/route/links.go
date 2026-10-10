package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) SetBookmark(ctx context.Context, req *chatimv1.SetBookmarkRequest) (*chatimv1.SetBookmarkResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.SetBookmarkResponse, error) {
		return api.SetBookmark(ctx, req)
	})
}

func (c *Client) GetReplies(ctx context.Context, req *chatimv1.GetRepliesRequest) (*chatimv1.GetRepliesResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.GetRepliesResponse, error) {
		return api.GetReplies(ctx, req)
	})
}

func (c *Client) ListBookmarks(ctx context.Context, req *chatimv1.ListBookmarksRequest) (*chatimv1.ListBookmarksResponse, Stats, error) {
	return call(ctx, c, c.loc.AnyAddr, retryIdempotent, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ListBookmarksResponse, error) {
		return api.ListBookmarks(ctx, req)
	})
}

func (c *Client) ListMentions(ctx context.Context, req *chatimv1.ListMentionsRequest) (*chatimv1.ListMentionsResponse, Stats, error) {
	return call(ctx, c, c.loc.AnyAddr, retryIdempotent, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ListMentionsResponse, error) {
		return api.ListMentions(ctx, req)
	})
}
