package callerid

import (
	"context"
	"fmt"

	"google.golang.org/grpc/metadata"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

const (
	TenantHeader = "x-chatim-tenant"
	UserHeader   = "x-chatim-user"
)

var (
	errNoCaller       = fmt.Errorf("caller tenant or user missing: %w", apperr.ErrUnauthenticated)
	errRepeatedCaller = fmt.Errorf("%w: caller tenant or user repeated", apperr.ErrInvalidArgument)
)

type Caller struct{ Tenant, User string }

func Of(ctx context.Context) (Caller, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	tenants, users := md.Get(TenantHeader), md.Get(UserHeader)
	switch {
	case len(tenants) == 0 || len(users) == 0:
		return Caller{}, errNoCaller
	case len(tenants) > 1 || len(users) > 1:
		return Caller{}, errRepeatedCaller
	}
	c := Caller{Tenant: tenants[0], User: users[0]}
	if err := ids.ValidTenant(c.Tenant); err != nil {
		return Caller{}, err
	}
	if err := ids.ValidUser(c.User); err != nil {
		return Caller{}, err
	}
	return c, nil
}
