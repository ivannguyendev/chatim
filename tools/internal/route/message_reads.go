package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) GetMessages(ctx context.Context, req *chatimv1.GetMessagesRequest) (*chatimv1.GetMessagesResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.GetMessagesResponse, error) {
		return api.GetMessages(ctx, req)
	})
}

func (c *Client) ListThreads(ctx context.Context, req *chatimv1.ListThreadsRequest) (*chatimv1.ListThreadsResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ListThreadsResponse, error) {
		return api.ListThreads(ctx, req)
	})
}

func (c *Client) GetReactions(ctx context.Context, req *chatimv1.GetReactionsRequest) (*chatimv1.GetReactionsResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.GetReactionsResponse, error) {
		return api.GetReactions(ctx, req)
	})
}

func (c *Client) ListPins(ctx context.Context, req *chatimv1.ListPinsRequest) (*chatimv1.ListPinsResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ListPinsResponse, error) {
		return api.ListPins(ctx, req)
	})
}
