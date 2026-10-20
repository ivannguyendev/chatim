package grpcsrv

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) GetReactions(ctx context.Context, req *chatimv1.GetReactionsRequest) (*chatimv1.GetReactionsResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	q := store.ReactorQuery{Key: store.MsgKey{Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq()}, Emoji: req.GetEmoji()}
	if err := s.interactionReader.CheckReactorFilter(q.Key, q.Emoji); err != nil {
		return nil, err
	}
	if q.After, err = decodeReactorCursor(req.GetAfter()); err != nil {
		return nil, err
	}
	page, err := s.interactionReader.Reactors(ctx, who.Tenant, who.User, q, int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.GetReactionsResponse{Reactors: make([]*chatimv1.Reactor, len(page.Reactors))}
	for i, r := range page.Reactors {
		resp.Reactors[i] = &chatimv1.Reactor{User: r.User, Emoji: r.Emoji, At: timestamppb.New(r.At)}
	}
	if page.Next != nil {
		resp.Next = encodeReactorCursor(*page.Next)
	}
	return resp, nil
}
