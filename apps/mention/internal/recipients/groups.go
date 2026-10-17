package recipients

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
)

func (r Resolver) groupsAmong(ctx context.Context, tenant string, roster []domain.Member, groups map[string]domain.Target, picked picks) error {
	if len(roster) == 0 {
		return nil
	}
	ids := make([]string, len(roster))
	for i, m := range roster {
		ids[i] = m.User
	}
	of, err := r.UserGroups.Of(ctx, tenant, ids)
	if err != nil {
		return err
	}
	for user, mine := range of {
		for _, g := range mine {
			if t, ok := groups[g]; ok {
				picked.offer(user, t)
			}
		}
	}
	return nil
}

func (r Resolver) groupsInRoom(ctx context.Context, tenant string, room uint64, groups map[string]domain.Target, picked picks) error {
	for id, t := range groups {
		if err := r.groupInRoom(ctx, tenant, room, id, t, picked); err != nil {
			return err
		}
	}
	return nil
}

func (r Resolver) groupInRoom(ctx context.Context, tenant string, room uint64, id string, t domain.Target, picked picks) error {
	after := ""
	for {
		page, err := r.UserGroups.MembersOfGroup(ctx, tenant, id, after, PageSize)
		if err != nil || len(page) == 0 {
			return err
		}
		members, err := r.Core.MembersOf(ctx, room, page)
		if err != nil {
			return err
		}
		for _, m := range active(members) {
			picked.offer(m.User, t)
		}
		if len(page) < PageSize {
			return nil
		}
		after = page[len(page)-1]
	}
}
