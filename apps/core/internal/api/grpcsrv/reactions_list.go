package grpcsrv

import (
	"context"
	"slices"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type ReactorLister interface {
	Reactors(ctx context.Context, q store.ReactorQuery) ([]store.Reactor, error)
}

func (s *Service) GetReactions(ctx context.Context, req *chatimv1.GetReactionsRequest) (*chatimv1.GetReactionsResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	q, err := s.reactorQueryOf(room, req)
	if err != nil {
		return nil, err
	}
	visible, err := s.reactionsVisible(ctx, who, q.Key)
	if err != nil {
		return nil, err
	}
	if !visible {
		return &chatimv1.GetReactionsResponse{}, nil
	}
	reactors, err := s.reactors.Reactors(ctx, q)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.GetReactionsResponse{Reactors: make([]*chatimv1.Reactor, len(reactors))}
	for i, r := range reactors {
		resp.Reactors[i] = &chatimv1.Reactor{User: r.User, Emoji: r.Emoji, At: timestamppb.New(r.At)}
	}
	if len(reactors) == q.Limit {
		resp.Next = encodeReactorCursor(q.Cursor(reactors[len(reactors)-1]))
	}
	return resp, nil
}

func (s *Service) reactorQueryOf(room uint64, req *chatimv1.GetReactionsRequest) (store.ReactorQuery, error) {
	q := store.ReactorQuery{Key: store.MsgKey{Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq()}, Emoji: req.GetEmoji()}
	if err := q.Key.Validate(); err != nil {
		return store.ReactorQuery{}, err
	}
	if err := domain.ValidateThread(q.Key.Thread); err != nil {
		return store.ReactorQuery{}, err
	}
	if q.Emoji != "" && !slices.Contains(s.interactionService.ReactionEmojis(), q.Emoji) {
		return store.ReactorQuery{}, domain.ErrEmojiNotAllowed
	}
	after, err := decodeReactorCursor(req.GetAfter())
	if err != nil {
		return store.ReactorQuery{}, err
	}
	q.After = after
	if q.Limit, err = domain.PageLimit(int(req.GetLimit())); err != nil {
		return store.ReactorQuery{}, err
	}
	return q, nil
}

func (s *Service) reactionsVisible(ctx context.Context, who callerid.Caller, key store.MsgKey) (bool, error) {
	grant, err := s.access.Admit(ctx, access.ReadReactions, who.Tenant, who.User, key.Room)
	if err != nil {
		return false, err
	}
	found, err := s.pages.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return false, err
	}
	if len(found) == 0 {
		return false, domain.ErrMessageNotFound
	}
	grant.Author, grant.Kind = found[0].From, found[0].Kind
	if err := s.access.Allow(ctx, grant); err != nil {
		return false, err
	}
	viewer, err := s.viewerOf(ctx, who.User, grant, store.PageQuery{Room: key.Room, Thread: key.Thread}, found)
	if err != nil {
		return false, err
	}
	return visibleToReader(s.maskView.Apply(viewer, found)[0]), nil
}
