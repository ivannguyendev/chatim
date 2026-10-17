package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) ListMyRooms(ctx context.Context, req *chatimv1.ListMyRoomsRequest) (*chatimv1.ListMyRoomsResponse, Stats, error) {
	return call(ctx, c, c.loc.AnyAddr, retryIdempotent, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ListMyRoomsResponse, error) {
		return api.ListMyRooms(ctx, req)
	})
}

func (c *Client) GetRooms(ctx context.Context, req *chatimv1.GetRoomsRequest) (*chatimv1.GetRoomsResponse, Stats, error) {
	return call(ctx, c, c.loc.AnyAddr, retryIdempotent, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.GetRoomsResponse, error) {
		return api.GetRooms(ctx, req)
	})
}

func (c *Client) UpdateRoom(ctx context.Context, req *chatimv1.UpdateRoomRequest) (*chatimv1.UpdateRoomResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.UpdateRoomResponse, error) {
		return api.UpdateRoom(ctx, req)
	})
}

func (c *Client) SetRoomPreferences(ctx context.Context, req *chatimv1.SetRoomPreferencesRequest) (*chatimv1.SetRoomPreferencesResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.SetRoomPreferencesResponse, error) {
		return api.SetRoomPreferences(ctx, req)
	})
}

func (c *Client) ListMembers(ctx context.Context, req *chatimv1.ListMembersRequest) (*chatimv1.ListMembersResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ListMembersResponse, error) {
		return api.ListMembers(ctx, req)
	})
}
