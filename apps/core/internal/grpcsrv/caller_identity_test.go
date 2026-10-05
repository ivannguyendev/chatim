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
	rg := &rig{rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(), hidden: memstore.NewHidden()}
	full := grpcsrv.Deps{Sender: &fakeSender{}, Rooms: rg.rooms, Pages: rg.msgs, Mutator: newMutator(t, rg, options{}), Edits: rg.edits, Hidden: rg.hidden}
	for name, drop := range map[string]func(d *grpcsrv.Deps){
		"no sender":  func(d *grpcsrv.Deps) { d.Sender = nil },
		"no rooms":   func(d *grpcsrv.Deps) { d.Rooms = nil },
		"no pages":   func(d *grpcsrv.Deps) { d.Pages = nil },
		"no mutator": func(d *grpcsrv.Deps) { d.Mutator = nil },
		"no edits":   func(d *grpcsrv.Deps) { d.Edits = nil },
		"no hidden":  func(d *grpcsrv.Deps) { d.Hidden = nil },
	} {
		deps := full
		drop(&deps)
		if _, err := grpcsrv.New(deps, quiet); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := grpcsrv.New(full, nil); err != nil {
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
		"EditMessage": func(ctx context.Context) error {
			_, err := rg.client.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: "42", Seq: 1, Text: "hi"})
			return err
		},
		"DeleteMessage": func(ctx context.Context) error {
			_, err := rg.client.DeleteMessage(ctx, &chatimv1.DeleteMessageRequest{RoomId: "42", Seq: 1})
			return err
		},
		"HideMessage": func(ctx context.Context) error {
			_, err := rg.client.HideMessage(ctx, &chatimv1.HideMessageRequest{RoomId: "42", Seq: 1})
			return err
		},
		"ClearHistory": func(ctx context.Context) error {
			_, err := rg.client.ClearHistory(ctx, &chatimv1.ClearHistoryRequest{RoomId: "42"})
			return err
		},
		"GetEditHistory": func(ctx context.Context) error {
			_, err := rg.client.GetEditHistory(ctx, &chatimv1.GetEditHistoryRequest{RoomId: "42", Seq: 1})
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
