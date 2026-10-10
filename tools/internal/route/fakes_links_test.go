package route_test

import (
	"context"

	"google.golang.org/grpc"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (f *fakeCore) SetBookmark(ctx context.Context, in *chatimv1.SetBookmarkRequest, _ ...grpc.CallOption) (*chatimv1.SetBookmarkResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.SetBookmarkResponse{Changed: in.GetOn()}, nil
}

func (f *fakeCore) GetReplies(ctx context.Context, in *chatimv1.GetRepliesRequest, _ ...grpc.CallOption) (*chatimv1.GetRepliesResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	reply := &chatimv1.Message{RoomId: in.GetRoomId(), Seq: in.GetSeq() + 1, ReplyTo: &chatimv1.ReplyRef{Seq: in.GetSeq()}}
	return &chatimv1.GetRepliesResponse{Messages: []*chatimv1.Message{reply}, Next: in.GetSeq() + 1}, nil
}

func (f *fakeCore) ListBookmarks(ctx context.Context, in *chatimv1.ListBookmarksRequest, _ ...grpc.CallOption) (*chatimv1.ListBookmarksResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	item := &chatimv1.BookmarkItem{Message: &chatimv1.Message{RoomId: "42", Seq: 3}, Available: true}
	return &chatimv1.ListBookmarksResponse{Items: []*chatimv1.BookmarkItem{item}, Next: "b1"}, nil
}

func (f *fakeCore) ListMentions(ctx context.Context, in *chatimv1.ListMentionsRequest, _ ...grpc.CallOption) (*chatimv1.ListMentionsResponse, error) {
	if err := f.record(ctx, in); err != nil {
		return nil, err
	}
	return &chatimv1.ListMentionsResponse{Messages: []*chatimv1.Message{{RoomId: "42", Seq: 4, MentionAll: true}}, Next: "m1"}, nil
}
