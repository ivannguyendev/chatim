package grpcsrv

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errBadMentionGroup = fmt.Errorf("%w: mention group", apperr.ErrInvalidArgument)

type MentionLister interface {
	List(ctx context.Context, q store.MentionQuery) ([]domain.Mention, error)
}

type MemberRoomLister interface {
	RoomsOf(ctx context.Context, tenant, user string) ([]uint64, error)
}

func (s *Service) ListMentions(ctx context.Context, req *chatimv1.ListMentionsRequest) (*chatimv1.ListMentionsResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	at, key, err := decodePageCursor(req.GetBefore())
	if err != nil {
		return nil, err
	}
	limit, err := domain.PageLimit(int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	targets, err := s.mentionWants(ctx, who, req.GetGroups())
	if err != nil {
		return nil, err
	}
	docs, err := s.mentionList.List(ctx, store.MentionQuery{Tenant: who.tenant, Targets: targets, Before: store.MentionCursor{At: at, Key: key}, Limit: limit})
	if err != nil {
		return nil, err
	}
	keys := mentionedKeys(docs)
	visible, err := s.visibleMessages(ctx, who, keys)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListMentionsResponse{}
	for _, k := range keys {
		if m, ok := visible[k]; ok {
			resp.Messages = append(resp.Messages, pbconv.Message(m))
		}
	}
	if len(docs) == limit {
		last := docs[len(docs)-1]
		resp.Next = encodePageCursor(last.CreatedAt, store.MsgKey(last.Key))
	}
	return resp, nil
}

func (s *Service) mentionWants(ctx context.Context, who caller, groups []*chatimv1.MentionGroup) ([]store.MentionWant, error) {
	since := make(map[string]time.Time, len(groups))
	var order []string
	for _, g := range groups {
		name, at, err := mentionGroup(g)
		if err != nil {
			return nil, err
		}
		prev, seen := since[name]
		switch {
		case !seen:
			order = append(order, name)
			since[name] = at
		case !prev.IsZero() && (at.IsZero() || at.Before(prev)):
			since[name] = at
		}
	}
	rooms, err := s.memberRooms.RoomsOf(ctx, who.tenant, who.user)
	if err != nil {
		return nil, err
	}
	out := make([]store.MentionWant, 0, 1+len(order)+len(rooms))
	out = append(out, store.MentionWant{Target: domain.MentionTarget{Kind: domain.MentionUser, ID: who.user}.Name(0)})
	for _, name := range order {
		out = append(out, store.MentionWant{Target: name, Since: since[name]})
	}
	for _, room := range rooms {
		out = append(out, store.MentionWant{Target: domain.MentionTarget{Kind: domain.MentionAll}.Name(room)})
	}
	return out, nil
}

func mentionGroup(g *chatimv1.MentionGroup) (string, time.Time, error) {
	target := domain.MentionTarget{Kind: domain.MentionGroup, ID: g.GetId()}
	if !domain.ValidMentionLink(target) {
		return "", time.Time{}, errBadMentionGroup
	}
	if g.GetSince() == nil {
		return target.Name(0), time.Time{}, nil
	}
	if err := g.GetSince().CheckValid(); err != nil {
		return "", time.Time{}, errBadMentionGroup
	}
	return target.Name(0), g.GetSince().AsTime().UTC(), nil
}

func mentionedKeys(docs []domain.Mention) []store.MsgKey {
	seen := make(map[store.MsgKey]struct{}, len(docs))
	out := make([]store.MsgKey, 0, len(docs))
	for _, d := range docs {
		k := store.MsgKey(d.Key)
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out
}
