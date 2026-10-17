package recipients

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

const (
	PageSize  = store.MaxGroupMemberPage
	SmallRoom = PageSize - 1
)

type Core interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
	ListMembers(ctx context.Context, room uint64, after domain.MemberCursor, limit int) ([]domain.Member, error)
}

type Resolver struct {
	Core       Core
	UserGroups store.UserGroups
	Catalog    Catalog
}

func (r Resolver) Resolve(ctx context.Context, tenant string, room uint64, roomType domain.RoomType, sender string, targets []domain.Target) (map[string]domain.Target, error) {
	picked := picks{sender: sender, chosen: make(map[string]domain.Target)}
	byUser, groups, everyone := split(targets)
	if err := r.users(ctx, room, byUser, picked); err != nil {
		return nil, err
	}
	var roster []domain.Member
	whole := everyone != nil && roomType != domain.RoomChannel
	if whole {
		var err error
		if roster, err = r.everyone(ctx, room); err != nil {
			return nil, err
		}
		for _, m := range roster {
			picked.offer(m.User, *everyone)
		}
	}
	groups, err := r.taggable(ctx, tenant, groups)
	if err != nil || len(groups) == 0 {
		return picked.chosen, err
	}
	small := len(roster) <= SmallRoom
	if !whole {
		if roster, err = r.Core.ListMembers(ctx, room, domain.MemberCursor{}, PageSize); err != nil {
			return nil, err
		}
		small = len(roster) <= SmallRoom
		roster = active(roster)
	}
	if small {
		err = r.groupsAmong(ctx, tenant, roster, groups, picked)
	} else {
		err = r.groupsInRoom(ctx, tenant, room, groups, picked)
	}
	if err != nil {
		return nil, err
	}
	return picked.chosen, nil
}

func (r Resolver) users(ctx context.Context, room uint64, targets map[string]domain.Target, picked picks) error {
	if len(targets) == 0 {
		return nil
	}
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	members, err := r.Core.MembersOf(ctx, room, ids)
	if err != nil {
		return err
	}
	for _, m := range active(members) {
		picked.offer(m.User, targets[m.User])
	}
	return nil
}

func (r Resolver) everyone(ctx context.Context, room uint64) ([]domain.Member, error) {
	var out []domain.Member
	after := domain.MemberCursor{}
	for {
		page, err := r.Core.ListMembers(ctx, room, after, PageSize)
		if err != nil {
			return nil, err
		}
		out = append(out, active(page)...)
		if len(page) < PageSize {
			return out, nil
		}
		after = page[len(page)-1].Cursor()
	}
}

func (r Resolver) taggable(ctx context.Context, tenant string, groups map[string]domain.Target) (map[string]domain.Target, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	ok, err := r.Catalog.Taggable(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make(map[string]domain.Target, len(groups))
	for id, t := range groups {
		if ok[id] {
			out[id] = t
		}
	}
	return out, nil
}
