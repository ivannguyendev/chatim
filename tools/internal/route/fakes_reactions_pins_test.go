package route_test

import (
	"context"

	"google.golang.org/grpc"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (f *fakeCore) ReactMessage(ctx context.Context, in *chatimv1.ReactMessageRequest, _ ...grpc.CallOption) (*chatimv1.ReactMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	counts := []*chatimv1.ReactionCount{{Emoji: in.GetEmoji(), Count: 1}}
	return &chatimv1.ReactMessageResponse{Change: 1, Reactions: &chatimv1.ReactionSummary{Counts: counts, Version: 1}}, nil
}

func (f *fakeCore) PinMessage(ctx context.Context, in *chatimv1.PinMessageRequest, _ ...grpc.CallOption) (*chatimv1.PinMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.PinMessageResponse{PinVersion: 1, Pins: []*chatimv1.Pin{{Seq: in.GetSeq(), PinVersion: 1}}}, nil
}

func (f *fakeCore) UnpinMessage(ctx context.Context, _ *chatimv1.UnpinMessageRequest, _ ...grpc.CallOption) (*chatimv1.UnpinMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.UnpinMessageResponse{PinVersion: 2}, nil
}
