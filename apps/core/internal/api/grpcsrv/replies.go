package grpcsrv

import (
	"cmp"
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type RepliesLister interface {
	Replies(ctx context.Context, parent store.MsgKey, afterSeq uint64, limit int) ([]domain.Reply, error)
}

func (s *Service) GetReplies(ctx context.Context, req *chatimv1.GetRepliesRequest) (*chatimv1.GetRepliesResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	parent := store.MsgKey{Room: room, Seq: req.GetSeq()}
	if err := parent.Validate(); err != nil {
		return nil, err
	}
	limit, err := domain.PageLimit(int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	grant, err := s.access.Admit(ctx, access.ReadReplies, who.tenant, who.user, room)
	if err != nil {
		return nil, err
	}
	found, err := s.pages.Find(ctx, room, []store.MsgKey{parent})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, domain.ErrMessageNotFound
	}
	grant.Author, grant.Kind = found[0].From, found[0].Kind
	if err := s.access.Allow(ctx, grant); err != nil {
		return nil, err
	}
	replies, err := s.replies.Replies(ctx, parent, req.GetAfter(), limit)
	if err != nil {
		return nil, err
	}
	msgs, err := s.replyMessages(ctx, who.user, grant, replies)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.GetRepliesResponse{Messages: make([]*chatimv1.Message, len(msgs))}
	for i, m := range msgs {
		resp.Messages[i] = pbconv.Message(m)
	}
	if len(replies) == limit {
		resp.Next = replies[len(replies)-1].Seq
	}
	return resp, nil
}

func (s *Service) replyMessages(ctx context.Context, user string, grant access.Request, replies []domain.Reply) ([]domain.Message, error) {
	if len(replies) == 0 {
		return nil, nil
	}
	keys := make([]store.MsgKey, len(replies))
	for i, r := range replies {
		keys[i] = store.ReplyKeyOf(r)
	}
	found, err := s.pages.Find(ctx, grant.Room.ID, keys)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(found, func(a, b domain.Message) int { return cmp.Compare(a.Seq, b.Seq) })
	viewer, err := s.viewerOf(ctx, user, grant, store.PageQuery{Room: grant.Room.ID}, found)
	if err != nil {
		return nil, err
	}
	return s.view.Apply(viewer, found), nil
}
