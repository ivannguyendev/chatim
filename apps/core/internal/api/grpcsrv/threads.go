package grpcsrv

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type ThreadLister interface {
	List(ctx context.Context, room uint64, before store.ReplyThreadCursor, limit int) ([]store.ReplyThread, error)
}

func (s *Service) ListThreads(ctx context.Context, req *chatimv1.ListThreadsRequest) (*chatimv1.ListThreadsResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	before, err := decodeThreadCursor(req.GetBefore())
	if err != nil {
		return nil, err
	}
	limit, err := domain.PageLimit(int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	grant, err := s.access.Authorize(ctx, access.ReadReplies, who.Tenant, who.User, room)
	if err != nil {
		return nil, err
	}
	threads, err := s.threads.List(ctx, room, before, limit)
	if err != nil {
		return nil, err
	}
	items, err := s.threadItems(ctx, who.User, grant, threads)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListThreadsResponse{Items: items}
	if len(threads) == limit {
		resp.Next = encodeThreadCursor(threads[len(threads)-1])
	}
	return resp, nil
}

func (s *Service) threadItems(ctx context.Context, user string, grant access.Request, threads []store.ReplyThread) ([]*chatimv1.ThreadItem, error) {
	if len(threads) == 0 {
		return nil, nil
	}
	asked := make(map[store.MsgKey]bool, 2*len(threads))
	keys := make([]store.MsgKey, 0, 2*len(threads))
	for _, t := range threads {
		for _, k := range []store.MsgKey{t.Parent, {Room: t.Parent.Room, Seq: t.LastReplySeq}} {
			if !asked[k] {
				asked[k] = true
				keys = append(keys, k)
			}
		}
	}
	found, err := s.pages.Find(ctx, grant.Room.ID, keys)
	if err != nil {
		return nil, err
	}
	masked, err := s.reader.Masked(ctx, user, grant, grant.Room.ID, 0, found)
	if err != nil {
		return nil, err
	}
	raw, shown := byKey(found), byKey(masked)
	items := make([]*chatimv1.ThreadItem, 0, len(threads))
	for _, t := range threads {
		if parent, ok := raw[t.Parent]; !ok || parent.Replies.N == 0 {
			continue
		}
		item := &chatimv1.ThreadItem{Message: pbconv.Message(shown[t.Parent]), LastReplyAt: timestamppb.New(t.LastReplyAt)}
		if reply, ok := shown[store.MsgKey{Room: t.Parent.Room, Seq: t.LastReplySeq}]; ok {
			item.LastReplyPreview = pbconv.Message(reply)
		}
		items = append(items, item)
	}
	return items, nil
}

func byKey(msgs []domain.Message) map[store.MsgKey]domain.Message {
	out := make(map[store.MsgKey]domain.Message, len(msgs))
	for _, m := range msgs {
		out[store.KeyOf(m)] = m
	}
	return out
}
