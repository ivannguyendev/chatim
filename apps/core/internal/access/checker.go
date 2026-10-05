package access

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Rooms interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
	Member(ctx context.Context, room uint64, user string) (domain.Member, error)
}

type Checker struct {
	rooms  Rooms
	policy Policy
}

func NewChecker(rooms Rooms, policy Policy) (*Checker, error) {
	if rooms == nil {
		return nil, fmt.Errorf("%w: access checker needs a room store", apperr.ErrInvalidArgument)
	}
	if policy == nil {
		policy = AllowMembers{}
	}
	return &Checker{rooms: rooms, policy: policy}, nil
}

func (c *Checker) Authorize(ctx context.Context, action Action, tenant, user string, room uint64) (Request, error) {
	r, err := c.rooms.Get(ctx, room)
	if err != nil {
		return Request{}, err
	}
	if err := domain.CheckTenant(r, tenant); err != nil {
		return Request{}, err
	}
	m, err := c.rooms.Member(ctx, room, user)
	if err != nil {
		return Request{}, err
	}
	req := Request{Action: action, User: user, Room: r, Member: m}
	return req, c.policy.Check(ctx, req)
}
