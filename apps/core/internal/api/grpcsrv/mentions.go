package grpcsrv

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const mentionPageMax = 20

var (
	errBadMentionGroup = fmt.Errorf("%w: mention group", apperr.ErrInvalidArgument)
	errBadMentionLimit = fmt.Errorf("%w: limit", apperr.ErrInvalidArgument)
)

type MentionLister interface {
	ListByTarget(ctx context.Context, q store.TargetMentionQuery) ([]domain.Mention, error)
	ListShared(ctx context.Context, q store.SharedMentionQuery) ([]domain.Mention, error)
}

type MemberRoomLister interface {
	RoomsOf(ctx context.Context, tenant, user string) ([]store.MemberRoom, error)
	SharedMentionTimes(ctx context.Context, rooms []uint64) (map[uint64]time.Time, error)
}

type mentionRead struct {
	who        callerid.Caller
	rooms      map[uint64]store.MemberRoom
	groups     map[string]time.Time
	groupIDs   []string
	groupSince time.Time
	hide       bool
	before     store.MentionCursor
	limit      int
	readAt     map[uint64]time.Time
	readKnown  map[uint64]bool
}

func (s *Service) ListMentions(ctx context.Context, req *chatimv1.ListMentionsRequest) (*chatimv1.ListMentionsResponse, error) {
	who, err := callerid.Of(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.mentionReadOf(who, req)
	if err != nil {
		return nil, err
	}
	rooms, err := s.memberRooms.RoomsOf(ctx, who.Tenant, who.User)
	if err != nil {
		return nil, err
	}
	r.rooms = make(map[uint64]store.MemberRoom, len(rooms))
	for _, room := range rooms {
		r.rooms[room.Room] = room
	}
	user, err := s.userMentions(ctx, r)
	if err != nil {
		return nil, err
	}
	shared, err := s.sharedMentions(ctx, r, rooms)
	if err != nil {
		return nil, err
	}
	page := store.PageMentions(r.limit, user, shared)
	if err := s.readPoints(ctx, r, mentionRooms(page.Items)); err != nil {
		return nil, err
	}
	for i := range page.Items {
		page.Items[i].Read = r.isRead(page.Items[i])
	}
	return s.mentionResponse(ctx, who, page)
}

func (s *Service) mentionReadOf(who callerid.Caller, req *chatimv1.ListMentionsRequest) (mentionRead, error) {
	at, key, err := decodePageCursor(req.GetBefore())
	if err != nil {
		return mentionRead{}, err
	}
	limit := int(req.GetLimit())
	switch {
	case limit == 0:
		limit = mentionPageMax
	case limit > mentionPageMax:
		return mentionRead{}, errBadMentionLimit
	}
	groups, err := mentionGroups(req.GetGroups())
	if err != nil {
		return mentionRead{}, err
	}
	_, hide := s.hideRead[who.Tenant]
	return mentionRead{
		who: who, groups: groups, groupIDs: slices.Sorted(maps.Keys(groups)), groupSince: earliestGroupSince(groups), hide: hide,
		before: store.MentionCursor{At: at, Key: key}, limit: limit, readAt: map[uint64]time.Time{}, readKnown: map[uint64]bool{},
	}, nil
}

func (s *Service) mentionResponse(ctx context.Context, who callerid.Caller, page store.MentionPage) (*chatimv1.ListMentionsResponse, error) {
	keys := make([]store.MsgKey, len(page.Items))
	for i, d := range page.Items {
		keys[i] = store.MsgKey(d.Key)
	}
	visible, err := s.VisibleMessages(ctx, who.Tenant, who.User, keys)
	if err != nil {
		return nil, err
	}
	resp := &chatimv1.ListMentionsResponse{}
	for i, d := range page.Items {
		if m, ok := visible[keys[i]]; ok {
			resp.Items = append(resp.Items, &chatimv1.MentionItem{Message: pbconv.Message(m), Read: d.Read})
		}
	}
	if !page.Next.At.IsZero() {
		resp.Next = encodePageCursor(page.Next.At, page.Next.Key)
	}
	return resp, nil
}

func mentionGroups(groups []*chatimv1.MentionGroup) (map[string]time.Time, error) {
	since := make(map[string]time.Time, len(groups))
	for _, g := range groups {
		id, at, err := mentionGroup(g)
		if err != nil {
			return nil, err
		}
		prev, seen := since[id]
		switch {
		case !seen:
			since[id] = at
		case !prev.IsZero() && (at.IsZero() || at.Before(prev)):
			since[id] = at
		}
	}
	return since, nil
}

func mentionGroup(g *chatimv1.MentionGroup) (string, time.Time, error) {
	target := domain.MentionTarget{Kind: domain.MentionGroup, ID: g.GetId()}
	if !domain.ValidMentionLink(target) {
		return "", time.Time{}, errBadMentionGroup
	}
	if g.GetSince() == nil {
		return target.ID, time.Time{}, nil
	}
	if err := g.GetSince().CheckValid(); err != nil {
		return "", time.Time{}, errBadMentionGroup
	}
	return target.ID, g.GetSince().AsTime().UTC(), nil
}

func earliestGroupSince(groups map[string]time.Time) time.Time {
	var out time.Time
	for _, since := range groups {
		if since.IsZero() {
			return time.Time{}
		}
		if out.IsZero() || since.Before(out) {
			out = since
		}
	}
	return out
}
