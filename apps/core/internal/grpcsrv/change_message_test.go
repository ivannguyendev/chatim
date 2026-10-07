package grpcsrv_test

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func roomNumber(t *testing.T, room string) uint64 {
	t.Helper()
	id, err := strconv.ParseUint(room, 10, 64)
	if err != nil {
		t.Fatalf("room id %q: %v", room, err)
	}
	return id
}

func TestEditAndDeleteThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	rg := newRig(t, options{events: events})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice := as(t, "acme", "alice")
	rg.send(t, alice, room, "c-1", "hi")
	edited, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, Text: "hello"})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if m := edited.GetMessage(); m.GetVer() != 1 || m.GetText() != "hello" || m.GetEditedAt() == nil || m.GetDeleted() {
		t.Fatalf("edited = %v, want version 1 with the new text", m)
	}
	deleted, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1, BaseVer: 1})
	if err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if m := deleted.GetMessage(); !m.GetDeleted() || m.GetText() != "" || m.GetVer() != 2 {
		t.Fatalf("deleted = %v, want a deleted message at version 2", m)
	}
	id := roomNumber(t, room)
	_, got := events.enqueued()
	var ids []string
	for _, ev := range got {
		ids = append(ids, ev.GetId())
	}
	want := []string{pbconv.RoomCreatedEventID(id), pbconv.MessageChangeEventID(id, 0, 1, 1), pbconv.MessageChangeEventID(id, 0, 1, 2)}
	if !slices.Equal(ids, want) {
		t.Fatalf("enqueued %v, want %v", ids, want)
	}
}

func TestChangeErrorsKeepTheirCodes(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	rg.send(t, alice, room, "c-1", "hi")
	if _, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, Text: "v1"}); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	edit := func(ctx context.Context, req *chatimv1.EditMessageRequest) func() error {
		return func() error { _, err := rg.client.EditMessage(ctx, req); return err }
	}
	cases := []struct {
		name string
		call func() error
		code codes.Code
	}{
		{"stale base", edit(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, Text: "v2"}), codes.FailedPrecondition},
		{"not the author", edit(bob, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVer: 1, Text: "v2"}), codes.PermissionDenied},
		{"unknown message", edit(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 9, Text: "v2"}), codes.NotFound},
		{"other tenant", edit(as(t, "other", "alice"), &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVer: 1, Text: "v2"}), codes.NotFound},
		{"bad room id", edit(alice, &chatimv1.EditMessageRequest{RoomId: "x", Seq: 1, Text: "v2"}), codes.InvalidArgument},
		{"empty text", edit(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVer: 1}), codes.InvalidArgument},
		{"thread", edit(alice, &chatimv1.EditMessageRequest{RoomId: room, ThreadRoot: 1, Seq: 1, BaseVer: 1, Text: "v2"}), codes.InvalidArgument},
		{"member deletes another's message", func() error {
			_, err := rg.client.DeleteMessage(bob, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1, BaseVer: 1})
			return err
		}, codes.PermissionDenied},
		{"hide an unknown message", func() error {
			_, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 9})
			return err
		}, codes.NotFound},
		{"clear by a stranger", func() error {
			_, err := rg.client.ClearHistory(as(t, "acme", "mallory"), &chatimv1.ClearHistoryRequest{RoomId: room})
			return err
		}, codes.PermissionDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { expectCode(t, c.call(), c.code) })
	}
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1, BaseVer: 1}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	expectCode(t, edit(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVer: 2, Text: "back"})(), codes.FailedPrecondition)
}
