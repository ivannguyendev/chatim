package grpcsrv_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestNewRequiresEveryDependency(t *testing.T) {
	rg := memStores()
	full := grpcsrv.Deps{
		Sender: &fakeSender{}, Rooms: rg.rooms, Pages: rg.msgs, Mutator: newMutator(t, rg, options{}), Edits: rg.edits, Hidden: rg.hidden, Bookmarks: rg.reactions,
		Members: rg.rooms, Timers: nopTimers{}, Requests: newRequests(t, nil), Directs: rg.directs,
	}
	for name, drop := range map[string]func(d *grpcsrv.Deps){
		"no sender":    func(d *grpcsrv.Deps) { d.Sender = nil },
		"no rooms":     func(d *grpcsrv.Deps) { d.Rooms = nil },
		"no pages":     func(d *grpcsrv.Deps) { d.Pages = nil },
		"no mutator":   func(d *grpcsrv.Deps) { d.Mutator = nil },
		"no edits":     func(d *grpcsrv.Deps) { d.Edits = nil },
		"no hidden":    func(d *grpcsrv.Deps) { d.Hidden = nil },
		"no bookmarks": func(d *grpcsrv.Deps) { d.Bookmarks = nil },
		"no members":   func(d *grpcsrv.Deps) { d.Members = nil },
		"no timers":    func(d *grpcsrv.Deps) { d.Timers = nil },
		"no requests":  func(d *grpcsrv.Deps) { d.Requests = nil },
		"no directs":   func(d *grpcsrv.Deps) { d.Directs = nil },
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
		"OpenDirectRoom": func(ctx context.Context) error {
			_, err := rg.client.OpenDirectRoom(ctx, &chatimv1.OpenDirectRoomRequest{OtherUser: "bob"})
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
		"ReactMessage": func(ctx context.Context) error {
			_, err := rg.client.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: "42", Seq: 1, Emoji: "👍"})
			return err
		},
		"PinMessage": func(ctx context.Context) error {
			_, err := rg.client.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: "42", Seq: 1})
			return err
		},
		"UnpinMessage": func(ctx context.Context) error {
			_, err := rg.client.UnpinMessage(ctx, &chatimv1.UnpinMessageRequest{RoomId: "42", Seq: 1})
			return err
		},
		"SetBookmark": func(ctx context.Context) error {
			_, err := rg.client.SetBookmark(ctx, &chatimv1.SetBookmarkRequest{RoomId: "42", Seq: 1, On: true})
			return err
		},
		"ListBookmarks": func(ctx context.Context) error {
			_, err := rg.client.ListBookmarks(ctx, &chatimv1.ListBookmarksRequest{})
			return err
		},
		"AddMembers": func(ctx context.Context) error {
			_, err := rg.client.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: "42", Users: []string{"bob"}, RequestId: "r-1"})
			return err
		},
		"RemoveMember": func(ctx context.Context) error {
			_, err := rg.client.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: "42", User: "bob"})
			return err
		},
		"LeaveRoom": func(ctx context.Context) error {
			_, err := rg.client.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: "42"})
			return err
		},
		"ChangeMemberRole": func(ctx context.Context) error {
			_, err := rg.client.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: "42", User: "bob"})
			return err
		},
		"SetMemberPriority": func(ctx context.Context) error {
			_, err := rg.client.SetMemberPriority(ctx, &chatimv1.SetMemberPriorityRequest{RoomId: "42", User: "bob", Priority: 1})
			return err
		},
		"MarkRead": func(ctx context.Context) error {
			_, err := rg.client.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: "42", Seq: 1})
			return err
		},
		"MarkUnread": func(ctx context.Context) error {
			_, err := rg.client.MarkUnread(ctx, &chatimv1.MarkUnreadRequest{RoomId: "42"})
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
