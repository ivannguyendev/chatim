package route_test

import (
	"context"
	"slices"

	"google.golang.org/grpc"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (f *fakeCore) ListMyRooms(ctx context.Context, in *chatimv1.ListMyRoomsRequest, _ ...grpc.CallOption) (*chatimv1.ListMyRoomsResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.ListMyRoomsResponse{Items: []*chatimv1.RoomItem{{Room: &chatimv1.Room{Id: "42"}, Unread: &chatimv1.Unread{Count: 3}}}, Next: "n1"}, nil
}

func (f *fakeCore) GetRooms(ctx context.Context, in *chatimv1.GetRoomsRequest, _ ...grpc.CallOption) (*chatimv1.GetRoomsResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	ids := in.GetRoomIds()
	return &chatimv1.GetRoomsResponse{Items: []*chatimv1.RoomItem{{Room: &chatimv1.Room{Id: ids[0]}}}, Missing: slices.Clone(ids[1:])}, nil
}

func (f *fakeCore) UpdateRoom(ctx context.Context, in *chatimv1.UpdateRoomRequest, _ ...grpc.CallOption) (*chatimv1.UpdateRoomResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.UpdateRoomResponse{Room: &chatimv1.Room{Id: in.GetRoomId(), Name: in.GetName(), InfoVer: 2}, Changed: true}, nil
}

func (f *fakeCore) SetRoomPreferences(ctx context.Context, in *chatimv1.SetRoomPreferencesRequest, _ ...grpc.CallOption) (*chatimv1.SetRoomPreferencesResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.SetRoomPreferencesResponse{Changed: in.GetPinned(), State: &chatimv1.MyRoomState{PreferenceVer: 1}}, nil
}

func (f *fakeCore) ListMembers(ctx context.Context, in *chatimv1.ListMembersRequest, _ ...grpc.CallOption) (*chatimv1.ListMembersResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.ListMembersResponse{Members: []*chatimv1.MemberItem{{User: "alice", Role: chatimv1.MemberRole_MEMBER_ROLE_OWNER}}, Next: "a1"}, nil
}

func (f *fakeCore) GetMessages(ctx context.Context, in *chatimv1.GetMessagesRequest, _ ...grpc.CallOption) (*chatimv1.GetMessagesResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	seqs := slices.Sorted(slices.Values(in.GetSeqs()))
	out := make([]*chatimv1.Message, len(seqs))
	for i, seq := range seqs {
		out[i] = &chatimv1.Message{RoomId: in.GetRoomId(), Seq: seq}
	}
	return &chatimv1.GetMessagesResponse{Messages: out}, nil
}

func (f *fakeCore) ListThreads(ctx context.Context, in *chatimv1.ListThreadsRequest, _ ...grpc.CallOption) (*chatimv1.ListThreadsResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.ListThreadsResponse{Items: []*chatimv1.ThreadItem{{Message: &chatimv1.Message{RoomId: in.GetRoomId(), Seq: 1}}}, Next: "t1"}, nil
}

func (f *fakeCore) GetReactions(ctx context.Context, in *chatimv1.GetReactionsRequest, _ ...grpc.CallOption) (*chatimv1.GetReactionsResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.GetReactionsResponse{Reactors: []*chatimv1.Reactor{{User: "bob", Emoji: in.GetEmoji()}}, Next: "r1"}, nil
}

func (f *fakeCore) ListPins(ctx context.Context, in *chatimv1.ListPinsRequest, _ ...grpc.CallOption) (*chatimv1.ListPinsResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	pin := &chatimv1.PinnedMessage{Pin: &chatimv1.Pin{Seq: 3}, Message: &chatimv1.Message{RoomId: in.GetRoomId(), Seq: 3}}
	return &chatimv1.ListPinsResponse{PinVer: 2, Items: []*chatimv1.PinnedMessage{pin}}, nil
}
