package route_test

import (
	"context"

	"google.golang.org/grpc"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (f *fakeCore) EditMessage(ctx context.Context, in *chatimv1.EditMessageRequest, _ ...grpc.CallOption) (*chatimv1.EditMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	m := &chatimv1.Message{RoomId: in.GetRoomId(), Seq: in.GetSeq(), Ver: in.GetBaseVer() + 1, Text: in.GetText()}
	return &chatimv1.EditMessageResponse{Message: m}, nil
}

func (f *fakeCore) DeleteMessage(ctx context.Context, in *chatimv1.DeleteMessageRequest, _ ...grpc.CallOption) (*chatimv1.DeleteMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	m := &chatimv1.Message{RoomId: in.GetRoomId(), Seq: in.GetSeq(), Ver: in.GetBaseVer() + 1, Deleted: true}
	return &chatimv1.DeleteMessageResponse{Message: m}, nil
}

func (f *fakeCore) HideMessage(ctx context.Context, _ *chatimv1.HideMessageRequest, _ ...grpc.CallOption) (*chatimv1.HideMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.HideMessageResponse{}, nil
}

func (f *fakeCore) ClearHistory(ctx context.Context, in *chatimv1.ClearHistoryRequest, _ ...grpc.CallOption) (*chatimv1.ClearHistoryResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.ClearHistoryResponse{ClearedBeforeSeq: in.GetUpToSeq()}, nil
}

func (f *fakeCore) GetEditHistory(ctx context.Context, in *chatimv1.GetEditHistoryRequest, _ ...grpc.CallOption) (*chatimv1.GetEditHistoryResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.GetEditHistoryResponse{Versions: []*chatimv1.MessageVersion{{Ver: in.GetAfterVer() + 1}}}, nil
}
