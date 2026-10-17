package route_test

import (
	"context"

	"google.golang.org/grpc"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (f *fakeCore) SetUserGroups(ctx context.Context, in *chatimv1.SetUserGroupsRequest, _ ...grpc.CallOption) (*chatimv1.SetUserGroupsResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	users := make([]string, 0, len(in.GetItems()))
	for _, it := range in.GetItems() {
		users = append(users, it.GetUser())
	}
	return &chatimv1.SetUserGroupsResponse{ChangedUsers: users}, nil
}

func (f *fakeCore) SetGroups(ctx context.Context, in *chatimv1.SetGroupsRequest, _ ...grpc.CallOption) (*chatimv1.SetGroupsResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	groups := make([]string, 0, len(in.GetItems()))
	for _, it := range in.GetItems() {
		groups = append(groups, it.GetId())
	}
	return &chatimv1.SetGroupsResponse{ChangedGroups: groups}, nil
}

func (f *fakeCore) ListRoomGroups(ctx context.Context, in *chatimv1.ListRoomGroupsRequest, _ ...grpc.CallOption) (*chatimv1.ListRoomGroupsResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.ListRoomGroupsResponse{Groups: []*chatimv1.RoomGroup{{Id: "team-sale", Name: "Sale"}}, Filtered: true}, nil
}
