package grpcsrv

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/callerid"
	"github.com/ivannguyendev/chatim/pkg/keys"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const MentionPageMax = 20

type mentionRead struct {
	who        callerid.Caller
	rooms      map[uint64]domain.MemberRoom
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
	r, err := s.mentionReadOf(ctx, who, req)
	if err != nil {
		return nil, err
	}
	rooms, err := s.core.RoomsOf(ctx, who.Tenant, who.User)
	if err != nil {
		return nil, dependencyErr("rooms of caller", err)
	}
	r.rooms = make(map[uint64]domain.MemberRoom, len(rooms))
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
	return s.mentionResponse(ctx, r, page, mentionReasons(user, shared))
}

func (s *Service) mentionReadOf(ctx context.Context, who callerid.Caller, req *chatimv1.ListMentionsRequest) (mentionRead, error) {
	before, err := decodePageCursor(req.GetBefore())
	if err != nil {
		return mentionRead{}, err
	}
	limit := int(req.GetLimit())
	switch {
	case limit == 0:
		limit = MentionPageMax
	case limit > MentionPageMax:
		return mentionRead{}, invalid("limit")
	}
	groups, err := mentionGroups(req.GetGroups())
	if err != nil {
		return mentionRead{}, err
	}
	if groups, err = s.taggableOnly(ctx, who.Tenant, groups); err != nil {
		return mentionRead{}, err
	}
	_, hide := s.hideRead[who.Tenant]
	return mentionRead{
		who: who, groups: groups, groupIDs: slices.Sorted(maps.Keys(groups)), groupSince: earliestGroupSince(groups), hide: hide,
		before: before, limit: limit, readAt: map[uint64]time.Time{}, readKnown: map[uint64]bool{},
	}, nil
}

func (s *Service) taggableOnly(ctx context.Context, tenant string, groups map[string]time.Time) (map[string]time.Time, error) {
	if len(groups) == 0 {
		return groups, nil
	}
	taggable, err := s.catalog.Taggable(ctx, tenant)
	if err != nil {
		return nil, dependencyErr("group catalog", err)
	}
	maps.DeleteFunc(groups, func(id string, _ time.Time) bool { return !taggable[id] })
	return groups, nil
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
	if !keys.ValidMentionID(g.GetId()) {
		return "", time.Time{}, invalid("mention group")
	}
	if g.GetSince() == nil {
		return g.GetId(), time.Time{}, nil
	}
	if err := g.GetSince().CheckValid(); err != nil {
		return "", time.Time{}, invalid("mention group")
	}
	return g.GetId(), g.GetSince().AsTime().UTC(), nil
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
