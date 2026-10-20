package grpcsrv

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) ListThreads(ctx context.Context, req *chatimv1.ListThreadsRequest) (*chatimv1.ListThreadsResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	before, err := decodeThreadCursor(req.GetBefore())
	if err != nil {
		return nil, err
	}
	page, err := s.interactionReader.Threads(ctx, who.Tenant, who.User, room, before, int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListThreadsResponse{}
	for _, t := range page.Items {
		item := &chatimv1.ThreadItem{Message: pbconv.Message(t.Parent), LastReplyAt: timestamppb.New(t.LastReplyAt)}
		if t.LastReply != nil {
			item.LastReplyPreview = pbconv.Message(*t.LastReply)
		}
		resp.Items = append(resp.Items, item)
	}
	if page.Next != nil {
		resp.Next = encodeThreadCursor(*page.Next)
	}
	return resp, nil
}
