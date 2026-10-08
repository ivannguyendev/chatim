package route_test

import (
	"context"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (f *fakeCore) record(ctx context.Context, in proto.Message) error {
	f.mu.Lock()
	f.seen = append(f.seen, in)
	f.mu.Unlock()
	return f.next(ctx)
}

func (f *fakeCore) requests() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

func (f *fakeCore) AddMembers(ctx context.Context, in *chatimv1.AddMembersRequest, _ ...grpc.CallOption) (*chatimv1.AddMembersResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	added := make([]*chatimv1.AddedMember, len(in.GetUsers()))
	for i, u := range in.GetUsers() {
		added[i] = &chatimv1.AddedMember{User: u, Ver: 1}
	}
	return &chatimv1.AddMembersResponse{Added: added}, nil
}

func (f *fakeCore) RemoveMember(ctx context.Context, in *chatimv1.RemoveMemberRequest, _ ...grpc.CallOption) (*chatimv1.RemoveMemberResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.RemoveMemberResponse{Changed: true, Ver: 2}, nil
}

func (f *fakeCore) LeaveRoom(ctx context.Context, in *chatimv1.LeaveRoomRequest, _ ...grpc.CallOption) (*chatimv1.LeaveRoomResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.LeaveRoomResponse{Changed: true, Ver: 2, NewOwner: "bob"}, nil
}

func (f *fakeCore) ChangeMemberRole(ctx context.Context, in *chatimv1.ChangeMemberRoleRequest, _ ...grpc.CallOption) (*chatimv1.ChangeMemberRoleResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.ChangeMemberRoleResponse{Changed: true, Ver: 2, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_MEMBER}, nil
}

func (f *fakeCore) SetMemberPriority(ctx context.Context, in *chatimv1.SetMemberPriorityRequest, _ ...grpc.CallOption) (*chatimv1.SetMemberPriorityResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.SetMemberPriorityResponse{Changed: true, Ver: 3, PreviousPriority: in.GetPriority() - 5}, nil
}

func (f *fakeCore) MarkRead(ctx context.Context, in *chatimv1.MarkReadRequest, _ ...grpc.CallOption) (*chatimv1.MarkReadResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.MarkReadResponse{ReadSeq: in.GetSeq(), ReadVer: 2}, nil
}

func (f *fakeCore) MarkUnread(ctx context.Context, in *chatimv1.MarkUnreadRequest, _ ...grpc.CallOption) (*chatimv1.MarkUnreadResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.MarkUnreadResponse{ReadSeq: in.GetSeq() - 1, ReadVer: 3}, nil
}
