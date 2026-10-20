package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) GetReplies(ctx context.Context, req *chatimv1.GetRepliesRequest) (*chatimv1.GetRepliesResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	parent := store.MsgKey{Room: room, Seq: req.GetSeq()}
	page, err := s.interactionReader.Replies(ctx, who.Tenant, who.User, parent, req.GetAfter(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	return &chatimv1.GetRepliesResponse{Messages: messagesOf(page.Messages), Next: page.Next}, nil
}
