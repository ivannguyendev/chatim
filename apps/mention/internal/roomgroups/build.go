package roomgroups

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const BuildChunk = 1000

type MemberIDs interface {
	RoomMemberIDs(ctx context.Context, room uint64) ([]string, error)
}

type UserGroupsOf interface {
	Of(ctx context.Context, tenant string, users []string) (map[string][]string, error)
}

type Builder struct {
	members    MemberIDs
	userGroups UserGroupsOf
}

func NewBuilder(members MemberIDs, userGroups UserGroupsOf) (Builder, error) {
	if members == nil || userGroups == nil {
		return Builder{}, fmt.Errorf("%w: room groups builder needs member ids and user groups", apperr.ErrInvalidArgument)
	}
	return Builder{members: members, userGroups: userGroups}, nil
}

func (b Builder) Build(ctx context.Context, tenant string, room uint64) (map[string]struct{}, error) {
	users, err := b.members.RoomMemberIDs(ctx, room)
	if err != nil {
		return nil, fmt.Errorf("room %d member ids: %w", room, err)
	}
	out := make(map[string]struct{})
	for start := 0; start < len(users); start += BuildChunk {
		of, err := b.userGroups.Of(ctx, tenant, users[start:min(len(users), start+BuildChunk)])
		if err != nil {
			return nil, fmt.Errorf("room %d user groups: %w", room, err)
		}
		for _, groups := range of {
			for _, g := range groups {
				out[g] = struct{}{}
			}
		}
	}
	return out, nil
}
