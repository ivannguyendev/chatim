package chatrpc_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/metadata"

	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func incoming(pairs ...string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
}

func TestCallerOfReadsTheTenantAndUserHeaders(t *testing.T) {
	got, err := chatrpc.CallerOf(incoming(chatrpc.TenantHeader, "acme", chatrpc.UserHeader, "Alice_1"))
	if err != nil || got != (chatrpc.Caller{Tenant: "acme", User: "Alice_1"}) {
		t.Fatalf("Of = %+v, %v; want acme/Alice_1", got, err)
	}
	if chatrpc.TenantHeader != "x-chatim-tenant" || chatrpc.UserHeader != "x-chatim-user" {
		t.Fatalf("headers = %q %q", chatrpc.TenantHeader, chatrpc.UserHeader)
	}
}

func TestCallerOfRefusesMissingRepeatedOrInvalidCallers(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"no metadata", context.Background(), apperr.ErrUnauthenticated},
		{"tenant only", incoming(chatrpc.TenantHeader, "acme"), apperr.ErrUnauthenticated},
		{"user only", incoming(chatrpc.UserHeader, "alice"), apperr.ErrUnauthenticated},
		{"tenant under another key", incoming("x-chatim-tenants", "acme", chatrpc.UserHeader, "alice"), apperr.ErrUnauthenticated},
		{"invalid tenant", incoming(chatrpc.TenantHeader, "Acme", chatrpc.UserHeader, "alice"), apperr.ErrInvalidArgument},
		{"invalid user", incoming(chatrpc.TenantHeader, "acme", chatrpc.UserHeader, "al.ice"), apperr.ErrInvalidArgument},
		{"repeated tenant", incoming(chatrpc.TenantHeader, "acme", chatrpc.TenantHeader, "other", chatrpc.UserHeader, "alice"), apperr.ErrInvalidArgument},
		{"repeated user", incoming(chatrpc.TenantHeader, "acme", chatrpc.UserHeader, "alice", chatrpc.UserHeader, "bob"), apperr.ErrInvalidArgument},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := chatrpc.CallerOf(c.ctx)
			if !errors.Is(err, c.want) || got != (chatrpc.Caller{}) {
				t.Fatalf("Of = %+v, %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestWithCallerAppendsTheCallerHeaders(t *testing.T) {
	md, _ := metadata.FromOutgoingContext(chatrpc.WithCaller(t.Context(), "acme", "alice"))
	if got := md.Get(chatrpc.TenantHeader); len(got) != 1 || got[0] != "acme" {
		t.Fatalf("tenant = %v", got)
	}
	if got := md.Get(chatrpc.UserHeader); len(got) != 1 || got[0] != "alice" {
		t.Fatalf("user = %v", got)
	}
}
