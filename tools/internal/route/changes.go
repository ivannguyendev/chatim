package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) EditMessage(ctx context.Context, req *chatimv1.EditMessageRequest) (*chatimv1.EditMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.EditMessageResponse, error) {
		return api.EditMessage(ctx, req)
	})
}

func (c *Client) DeleteMessage(ctx context.Context, req *chatimv1.DeleteMessageRequest) (*chatimv1.DeleteMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.DeleteMessageResponse, error) {
		return api.DeleteMessage(ctx, req)
	})
}

func (c *Client) HideMessage(ctx context.Context, req *chatimv1.HideMessageRequest) (*chatimv1.HideMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.HideMessageResponse, error) {
		return api.HideMessage(ctx, req)
	})
}

func (c *Client) ClearHistory(ctx context.Context, req *chatimv1.ClearHistoryRequest) (*chatimv1.ClearHistoryResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ClearHistoryResponse, error) {
		return api.ClearHistory(ctx, req)
	})
}

func (c *Client) GetEditHistory(ctx context.Context, req *chatimv1.GetEditHistoryRequest) (*chatimv1.GetEditHistoryResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.GetEditHistoryResponse, error) {
		return api.GetEditHistory(ctx, req)
	})
}
