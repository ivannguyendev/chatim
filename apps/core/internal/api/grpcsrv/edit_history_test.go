package grpcsrv_test

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
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
		out[i] = versionRow{v.GetVer(), v.GetKind(), v.GetText(), v.GetBy()}
	}
	return out
}

func (rg *rig) editTwice(t *testing.T, ctx context.Context, room string) {
	t.Helper()
	for _, e := range []struct {
		base uint32
		text string
	}{{0, "v1"}, {1, "v2"}} {
		if _, err := rg.client.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVer: e.base, Text: e.text}); err != nil {
			t.Fatalf("EditMessage(%s): %v", e.text, err)
		}
	}
}

func TestEditHistoryStartsWithTheOriginalRow(t *testing.T) {
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
}

func TestEditHistoryAfterAVerSkipsTheOriginalRow(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	alice := as(t, "acme", "alice")
	rg.send(t, alice, room, "c-1", "v0")
	rg.editTwice(t, alice, room)
	cases := []struct {
		after, limit uint32
		want         []versionRow
	}{
		{0, 1, []versionRow{{0, chatimv1.EditKind_EDIT_KIND_ORIGINAL, "v0", "alice"}, {1, chatimv1.EditKind_EDIT_KIND_TEXT, "v1", "alice"}}},
		{1, 10, []versionRow{{2, chatimv1.EditKind_EDIT_KIND_TEXT, "v2", "alice"}}},
		{2, 10, []versionRow{}},
	}
	for _, c := range cases {
		resp, err := rg.client.GetEditHistory(alice, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1, AfterVer: c.after, Limit: c.limit})
		if got := versionRows(resp.GetVersions()); err != nil || !slices.Equal(got, c.want) {
			t.Fatalf("after %d limit %d = %+v, %v; want %+v", c.after, c.limit, got, err, c.want)
		}
	}
}

func TestEditHistoryHidesALoneOriginalRow(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	alice := as(t, "acme", "alice")
	sent := rg.send(t, alice, room, "c-1", "v0")
	row := domain.Edit{
		Room: roomNumber(t, room), Seq: 1, Version: 0, Kind: domain.EditOriginal, Tenant: "acme", By: "alice", Text: "v0", At: sent.GetCreatedAt().AsTime(),
	}
	if err := rg.edits.Append(t.Context(), row); err != nil {
		t.Fatalf("Append original: %v", err)
	}
	resp, err := rg.client.GetEditHistory(alice, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1})
	if err != nil || len(resp.GetVersions()) != 0 {
		t.Fatalf("GetEditHistory = %v, %v; want no versions while only the original row exists", resp, err)
	}
}

func TestEditHistoryOfADeletedMessageIsEmpty(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	alice := as(t, "acme", "alice")
	rg.send(t, alice, room, "c-1", "v0")
	rg.editTwice(t, alice, room)
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1, BaseVer: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	resp, err := rg.client.GetEditHistory(alice, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1})
	if err != nil || len(resp.GetVersions()) != 0 {
		t.Fatalf("GetEditHistory = %v, %v; want no versions", resp, err)
	}
}

func TestEditHistoryIsEmptyOnceADeleteFactIsStoredBeforeItsProjection(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	alice := as(t, "acme", "alice")
	sent := rg.send(t, alice, room, "c-1", "v0")
	rg.editTwice(t, alice, room)
	fact := domain.Edit{Room: roomNumber(t, room), Seq: 1, Version: 3, Kind: domain.EditDelete, Tenant: "acme", By: "alice", At: sent.GetCreatedAt().AsTime()}
	if err := rg.edits.Append(t.Context(), fact); err != nil {
		t.Fatalf("Append delete fact: %v", err)
	}
	for _, limit := range []uint32{0, 1, 3} {
		resp, err := rg.client.GetEditHistory(alice, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1, Limit: limit})
		if err != nil || len(resp.GetVersions()) != 0 {
			t.Fatalf("limit %d: GetEditHistory = %v, %v; want no versions while the delete waits for its projection", limit, resp, err)
		}
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
