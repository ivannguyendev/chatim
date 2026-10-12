package grpcsrv_test

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func forwardReq(room, cid, from string, seq uint64) *chatimv1.SendMessageRequest {
	return &chatimv1.SendMessageRequest{RoomId: room, Cid: cid, Text: "client text", ForwardFrom: &chatimv1.ForwardRef{RoomId: from, Seq: seq}}
}

func (rg *rig) forward(t *testing.T, ctx context.Context, req *chatimv1.SendMessageRequest) {
	t.Helper()
	if _, err := rg.client.SendMessage(ctx, req); err != nil {
		t.Fatalf("forward %s: %v", req.GetCid(), err)
	}
}

func TestForwardCopiesTheCurrentSourceTextWithoutClientTextOrMentions(t *testing.T) {
	rg := newRig(t, options{})
	src := rg.createGroup(t, "acme", "alice", "bob")
	dst := rg.createGroup(t, "acme", "alice", "bob", "carol")
	rg.send(t, as(t, "acme", "alice"), src, "c-1", "draft")
	if _, err := rg.client.EditMessage(as(t, "acme", "alice"), &chatimv1.EditMessageRequest{RoomId: src, Seq: 1, Text: "final"}); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	req := forwardReq(dst, "c-1", src, 1)
	req.Mentions = &chatimv1.MentionSet{All: true, Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "carol"}}}
	rg.forward(t, as(t, "acme", "bob"), req)
	source := historyOf(t, rg, src)[0]
	got := historyOf(t, rg, dst)[0]
	want := &chatimv1.ForwardRef{RoomId: src, Seq: 1, Author: "alice", SentAt: source.GetCreatedAt()}
	if got.GetSender() != "bob" || got.GetText() != "final" || !proto.Equal(got.GetForwardFrom(), want) ||
		got.GetMentionAll() || len(got.GetMentionTargets()) != 0 || got.GetReplyTo() != nil {
		t.Fatalf("forwarded = %v, want bob's copy of %q from %v without mentions", got, "final", want)
	}
}

func TestForwardOfAForwardKeepsTheOriginalAuthor(t *testing.T) {
	rg := newRig(t, options{})
	src := rg.createGroup(t, "acme", "alice", "bob")
	mid := rg.createGroup(t, "acme", "alice", "bob", "carol")
	rg.send(t, as(t, "acme", "alice"), src, "c-1", "origin")
	rg.forward(t, as(t, "acme", "bob"), forwardReq(mid, "c-1", src, 1))
	rg.forward(t, as(t, "acme", "carol"), forwardReq(mid, "c-2", mid, 1))
	msgs := historyOf(t, rg, mid)
	first, second := msgs[0].GetForwardFrom(), msgs[1].GetForwardFrom()
	if msgs[1].GetSender() != "carol" || msgs[1].GetText() != "origin" || !proto.Equal(first, second) || second.GetRoomId() != src || second.GetAuthor() != "alice" {
		t.Fatalf("second forward = %v, want carol's copy pointing at alice's %s/1 like %v", msgs[1], src, first)
	}
}

func TestForwardRefusesSourcesTheCallerCannotSee(t *testing.T) {
	rg := newRig(t, options{})
	src := rg.createGroup(t, "acme", "alice", "bob")
	dst := rg.createGroup(t, "acme", "alice", "bob", "carol")
	foreign := rg.createGroup(t, "other", "alice", "bob")
	for i, text := range []string{"one", "two", "three"} {
		rg.send(t, as(t, "acme", "alice"), src, fmt.Sprintf("c-%d", i+1), text)
	}
	rg.send(t, as(t, "other", "alice"), foreign, "c-1", "foreign")
	if _, err := rg.client.DeleteMessage(as(t, "acme", "alice"), &chatimv1.DeleteMessageRequest{RoomId: src, Seq: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if _, err := rg.client.HideMessage(as(t, "acme", "bob"), &chatimv1.HideMessageRequest{RoomId: src, Seq: 3}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	cases := []struct {
		name, user, from string
		seq              uint64
		want             codes.Code
	}{
		{"not a member of the source", "carol", src, 1, codes.PermissionDenied},
		{"deleted source", "bob", src, 2, codes.FailedPrecondition},
		{"hidden by the caller", "bob", src, 3, codes.NotFound},
		{"missing seq", "bob", src, 9, codes.NotFound},
		{"missing room", "bob", "77", 1, codes.NotFound},
		{"other tenant", "alice", foreign, 1, codes.NotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := rg.client.SendMessage(as(t, "acme", tc.user), forwardReq(dst, "c-9", tc.from, tc.seq))
			expectCode(t, err, tc.want)
		})
	}
	if n := len(historyOf(t, rg, dst)); n != 0 {
		t.Fatalf("destination has %d messages, want 0", n)
	}
}

func TestForwardRefusesMessagesClearedByTheCaller(t *testing.T) {
	rg := newRig(t, options{})
	src := rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "alice"), src, "c-1", "old")
	if _, err := rg.client.ClearHistory(as(t, "acme", "bob"), &chatimv1.ClearHistoryRequest{RoomId: src}); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	_, err := rg.client.SendMessage(as(t, "acme", "bob"), forwardReq(src, "c-2", src, 1))
	expectCode(t, err, codes.NotFound)
	rg.forward(t, as(t, "acme", "alice"), forwardReq(src, "c-2", src, 1))
}

func TestForwardRejectsBadSources(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "alice"), room, "c-1", "parent")
	cases := map[string]*chatimv1.ForwardRef{
		"thread source": {RoomId: room, ThreadRoot: 1, Seq: 1},
		"seq 0":         {RoomId: room},
		"bad room id":   {RoomId: "x", Seq: 1},
	}
	for name, ref := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.SendMessage(as(t, "acme", "bob"), &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-2", ForwardFrom: ref})
			expectCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestForwardAsksThePolicyWithTheSourceAuthorAndKind(t *testing.T) {
	var seen access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error {
		if r.Action == access.ForwardMessage {
			seen = r
			return access.ErrDenied
		}
		return nil
	})
	rg := newRig(t, options{policy: deny})
	src := rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "alice"), src, "c-1", "secret")
	_, err := rg.client.SendMessage(as(t, "acme", "bob"), forwardReq(src, "c-2", src, 1))
	expectCode(t, err, codes.PermissionDenied)
	if seen.User != "bob" || seen.Author != "alice" || seen.Kind != domain.KindText || seen.Room.Tenant != "acme" {
		t.Fatalf("policy saw %+v, want bob forwarding alice's text message", seen)
	}
}
