package grpcsrv_test

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type versionRow struct {
	version  uint32
	kind     chatimv1.EditKind
	text, by string
}

func versionRows(vs []*chatimv1.MessageVersion) []versionRow {
	out := make([]versionRow, len(vs))
	for i, v := range vs {
		out[i] = versionRow{v.GetVersion(), v.GetKind(), v.GetText(), v.GetBy()}
	}
	return out
}

func (rg *rig) editTwice(t *testing.T, ctx context.Context, room string) {
	t.Helper()
	for _, e := range []struct {
		base uint32
		text string
	}{{0, "v1"}, {1, "v2"}} {
		if _, err := rg.client.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVersion: e.base, Text: e.text}); err != nil {
			t.Fatalf("EditMessage(%s): %v", e.text, err)
		}
	}
}

func TestEditHistoryListsEveryVersion(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	sent := rg.send(t, alice, room, "c-1", "v0")
	rg.editTwice(t, alice, room)
	resp, err := rg.client.GetEditHistory(bob, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1})
	if err != nil {
		t.Fatalf("GetEditHistory: %v", err)
	}
	want := []versionRow{
		{0, chatimv1.EditKind_EDIT_KIND_ORIGINAL, "v0", "alice"},
		{1, chatimv1.EditKind_EDIT_KIND_TEXT, "v1", "alice"},
		{2, chatimv1.EditKind_EDIT_KIND_TEXT, "v2", "alice"},
	}
	if got := versionRows(resp.GetVersions()); !slices.Equal(got, want) {
		t.Fatalf("versions = %+v, want %+v", got, want)
	}
	if at := resp.GetVersions()[0].GetAt(); !proto.Equal(at, sent.GetCreatedAt()) {
		t.Fatalf("original at = %v, want the send time %v", at, sent.GetCreatedAt())
	}
	after, err := rg.client.GetEditHistory(bob, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1, AfterVersion: 1})
	if err != nil || !slices.Equal(versionRows(after.GetVersions()), want[2:]) {
		t.Fatalf("after v1 = %v, %v; want only v2", after, err)
	}
}

func TestEditHistoryOfADeletedMessageIsEmpty(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	alice := as(t, "acme", "alice")
	rg.send(t, alice, room, "c-1", "v0")
	rg.editTwice(t, alice, room)
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1, BaseVersion: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	resp, err := rg.client.GetEditHistory(alice, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1})
	if err != nil || len(resp.GetVersions()) != 0 {
		t.Fatalf("GetEditHistory = %v, %v; want no versions", resp, err)
	}
}

func TestEditHistoryChecksInputAndAccess(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	rg.send(t, as(t, "acme", "alice"), room, "c-1", "v0")
	cases := []struct {
		name   string
		tenant string
		user   string
		req    *chatimv1.GetEditHistoryRequest
		code   codes.Code
	}{
		{"unknown message", "acme", "alice", &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 9}, codes.NotFound},
		{"other tenant", "other", "alice", &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1}, codes.NotFound},
		{"not a member", "acme", "mallory", &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1}, codes.PermissionDenied},
		{"seq zero", "acme", "alice", &chatimv1.GetEditHistoryRequest{RoomId: room}, codes.InvalidArgument},
		{"thread", "acme", "alice", &chatimv1.GetEditHistoryRequest{RoomId: room, ThreadRoot: 1, Seq: 1}, codes.InvalidArgument},
		{"limit over 100", "acme", "alice", &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1, Limit: 101}, codes.InvalidArgument},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := rg.client.GetEditHistory(as(t, c.tenant, c.user), c.req)
			expectCode(t, err, c.code)
		})
	}
}

func TestEditHistoryAsksThePolicyWithTheAuthor(t *testing.T) {
	var got access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { got = r; return access.ErrDenied })
	rg := newRig(t, options{policy: deny})
	room := rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "bob"), room, "c-1", "v0")
	_, err := rg.client.GetEditHistory(as(t, "acme", "alice"), &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1})
	expectCode(t, err, codes.PermissionDenied)
	if got.Action != access.ReadEditHistory || got.User != "alice" || got.Author != "bob" {
		t.Fatalf("policy saw %+v, want read_edit_history by alice on bob's message", got)
	}
}
