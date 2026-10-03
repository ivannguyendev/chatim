package grpcsrv_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestNewRequiresEveryDependency(t *testing.T) {
	rooms, msgs, sender := memstore.NewRooms(), memstore.NewMessages(), &fakeSender{}
	cases := map[string]grpcsrv.Deps{
		"no sender": {Rooms: rooms, Pages: msgs},
		"no rooms":  {Sender: sender, Pages: msgs},
		"no pages":  {Sender: sender, Rooms: rooms},
	}
	for name, deps := range cases {
		if _, err := grpcsrv.New(deps, quiet); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := grpcsrv.New(grpcsrv.Deps{Sender: sender, Rooms: rooms, Pages: msgs}, nil); err != nil {
		t.Errorf("New with defaults = %v, want nil", err)
	}
}

func TestEveryRPCChecksCallerIdentityFirst(t *testing.T) {
	sender := &fakeSender{}
	rg := newRig(t, options{sender: sender})
	rpcs := map[string]func(context.Context) error{
		"CreateRoom": func(ctx context.Context) error {
			_, err := rg.client.CreateRoom(ctx, &chatimv1.CreateRoomRequest{})
			return err
		},
		"SendMessage": func(ctx context.Context) error {
			_, err := rg.client.SendMessage(ctx, &chatimv1.SendMessageRequest{RoomId: "42", Cid: "c-1", Text: "hi"})
			return err
		},
		"GetHistory": func(ctx context.Context) error {
			_, err := rg.client.GetHistory(ctx, &chatimv1.GetHistoryRequest{RoomId: "42"})
			return err
		},
	}
	callers := []struct {
		name  string
		pairs []string
		code  codes.Code
	}{
		{"no metadata", nil, codes.Unauthenticated},
		{"tenant only", []string{grpcsrv.TenantHeader, "acme"}, codes.Unauthenticated},
		{"user only", []string{grpcsrv.UserHeader, "alice"}, codes.Unauthenticated},
		{"tenant under another key", []string{"x-chatim-tenants", "acme", grpcsrv.UserHeader, "alice"}, codes.Unauthenticated},
		{"invalid tenant", []string{grpcsrv.TenantHeader, "Acme", grpcsrv.UserHeader, "alice"}, codes.InvalidArgument},
		{"invalid user", []string{grpcsrv.TenantHeader, "acme", grpcsrv.UserHeader, "al.ice"}, codes.InvalidArgument},
		{"repeated tenant", []string{grpcsrv.TenantHeader, "acme", grpcsrv.TenantHeader, "other", grpcsrv.UserHeader, "alice"}, codes.InvalidArgument},
		{"repeated user", []string{grpcsrv.TenantHeader, "acme", grpcsrv.UserHeader, "alice", grpcsrv.UserHeader, "bob"}, codes.InvalidArgument},
	}
	for rpc, call := range rpcs {
		for _, c := range callers {
			t.Run(rpc+"/"+c.name, func(t *testing.T) {
				ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(c.pairs...))
				expectCode(t, call(ctx), c.code)
			})
		}
	}
	if got := sender.sent(); len(got) != 0 {
		t.Fatalf("sender reached by %d unidentified calls", len(got))
	}
}
