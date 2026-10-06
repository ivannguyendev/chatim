package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) ReactMessage(ctx context.Context, req *chatimv1.ReactMessageRequest) (*chatimv1.ReactMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ReactMessageResponse, error) {
		return api.ReactMessage(ctx, req)
	})
}

func (c *Client) PinMessage(ctx context.Context, req *chatimv1.PinMessageRequest) (*chatimv1.PinMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.PinMessageResponse, error) {
		return api.PinMessage(ctx, req)
	})
}

func (c *Client) UnpinMessage(ctx context.Context, req *chatimv1.UnpinMessageRequest) (*chatimv1.UnpinMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.UnpinMessageResponse, error) {
		return api.UnpinMessage(ctx, req)
	})
}

func (c *Client) GetReactionSettings(ctx context.Context, req *chatimv1.GetReactionSettingsRequest) (*chatimv1.GetReactionSettingsResponse, Stats, error) {
	return call(ctx, c, c.loc.AnyAddr, retryIdempotent, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.GetReactionSettingsResponse, error) {
		return api.GetReactionSettings(ctx, req)
	})
}
