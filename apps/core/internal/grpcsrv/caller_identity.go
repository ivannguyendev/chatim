package grpcsrv

import (
	"context"
	"fmt"

	"google.golang.org/grpc/metadata"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	TenantHeader = "x-chatim-tenant"
	UserHeader   = "x-chatim-user"
)

var (
	errNoCaller       = fmt.Errorf("caller tenant or user missing: %w", apperr.ErrUnauthenticated)
	errRepeatedCaller = fmt.Errorf("%w: caller tenant or user repeated", apperr.ErrInvalidArgument)
)

type caller struct{ tenant, user string }

func callerOf(ctx context.Context) (caller, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	tenants, users := md.Get(TenantHeader), md.Get(UserHeader)
	switch {
	case len(tenants) == 0 || len(users) == 0:
		return caller{}, errNoCaller
	case len(tenants) > 1 || len(users) > 1:
		return caller{}, errRepeatedCaller
	}
	c := caller{tenant: tenants[0], user: users[0]}
	if err := domain.ValidTenant(c.tenant); err != nil {
		return caller{}, err
	}
	if err := domain.ValidUser(c.user); err != nil {
		return caller{}, err
	}
	return c, nil
}
