package callerid_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/metadata"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/callerid"
)

func incoming(pairs ...string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
}

func TestOfReadsTheTenantAndUserHeaders(t *testing.T) {
	got, err := callerid.Of(incoming(callerid.TenantHeader, "acme", callerid.UserHeader, "Alice_1"))
	if err != nil || got != (callerid.Caller{Tenant: "acme", User: "Alice_1"}) {
		t.Fatalf("Of = %+v, %v; want acme/Alice_1", got, err)
	}
	if callerid.TenantHeader != "x-chatim-tenant" || callerid.UserHeader != "x-chatim-user" {
		t.Fatalf("headers = %q %q", callerid.TenantHeader, callerid.UserHeader)
	}
}

func TestOfRefusesMissingRepeatedOrInvalidCallers(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"no metadata", context.Background(), apperr.ErrUnauthenticated},
		{"tenant only", incoming(callerid.TenantHeader, "acme"), apperr.ErrUnauthenticated},
		{"user only", incoming(callerid.UserHeader, "alice"), apperr.ErrUnauthenticated},
		{"tenant under another key", incoming("x-chatim-tenants", "acme", callerid.UserHeader, "alice"), apperr.ErrUnauthenticated},
		{"invalid tenant", incoming(callerid.TenantHeader, "Acme", callerid.UserHeader, "alice"), apperr.ErrInvalidArgument},
		{"invalid user", incoming(callerid.TenantHeader, "acme", callerid.UserHeader, "al.ice"), apperr.ErrInvalidArgument},
		{"repeated tenant", incoming(callerid.TenantHeader, "acme", callerid.TenantHeader, "other", callerid.UserHeader, "alice"), apperr.ErrInvalidArgument},
		{"repeated user", incoming(callerid.TenantHeader, "acme", callerid.UserHeader, "alice", callerid.UserHeader, "bob"), apperr.ErrInvalidArgument},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := callerid.Of(c.ctx)
			if !errors.Is(err, c.want) || got != (callerid.Caller{}) {
				t.Fatalf("Of = %+v, %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestWithCallerAppendsTheCallerHeaders(t *testing.T) {
	md, _ := metadata.FromOutgoingContext(callerid.WithCaller(t.Context(), "acme", "alice"))
	if got := md.Get(callerid.TenantHeader); len(got) != 1 || got[0] != "acme" {
		t.Fatalf("tenant = %v", got)
	}
	if got := md.Get(callerid.UserHeader); len(got) != 1 || got[0] != "alice" {
		t.Fatalf("user = %v", got)
	}
}
