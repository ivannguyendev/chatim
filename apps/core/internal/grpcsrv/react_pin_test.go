package grpcsrv_test

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestReactAndPinThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	rg := newRig(t, options{events: events})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	rg.send(t, alice, room, "c-1", "hi")
	first, err := rg.client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "👍"})
	if err != nil {
		t.Fatalf("bob ReactMessage: %v", err)
	}
	second, err := rg.client.ReactMessage(alice, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "👍"})
	if err != nil {
		t.Fatalf("alice ReactMessage: %v", err)
	}
	want := pbconv.ReactionSummary(domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 2}}, Version: 2})
	if first.GetChange() != 1 || second.GetChange() != 1 || !proto.Equal(second.GetReactions(), want) {
		t.Fatalf("reactions = %v then %v, want change 1 each and %v", first, second, want)
	}
	pinned, err := rg.client.PinMessage(bob, &chatimv1.PinMessageRequest{RoomId: room, Seq: 1})
	if err != nil || pinned.GetPinVer() != 1 || len(pinned.GetPins()) != 1 {
		t.Fatalf("PinMessage = %v, %v; want one pin at version 1", pinned, err)
	}
	if p := pinned.GetPins()[0]; p.GetSeq() != 1 || p.GetBy() != "bob" || p.GetPinVer() != 1 || p.GetPinnedAt() == nil {
		t.Fatalf("pin = %v, want seq 1 pinned by bob at version 1", p)
	}
	unpinned, err := rg.client.UnpinMessage(alice, &chatimv1.UnpinMessageRequest{RoomId: room, Seq: 1})
	if err != nil || unpinned.GetPinVer() != 2 || len(unpinned.GetPins()) != 0 {
		t.Fatalf("UnpinMessage = %v, %v; want no pins at version 2", unpinned, err)
	}
	id := roomNumber(t, room)
	_, got := events.enqueued()
	var ids []string
	for _, ev := range got {
		ids = append(ids, ev.GetId())
	}
	wantIDs := []string{
		pbconv.RoomCreatedEventID(id),
		pbconv.ReactionEventID(id, 0, 1, "bob", 1), pbconv.ReactionCountsEventID(id, 0, 1, 1),
		pbconv.ReactionEventID(id, 0, 1, "alice", 1), pbconv.ReactionCountsEventID(id, 0, 1, 2),
		pbconv.PinEventID(id, 1), pbconv.PinEventID(id, 2),
	}
	if !slices.Equal(ids, wantIDs) {
		t.Fatalf("enqueued %v, want %v", ids, wantIDs)
	}
}

func TestReactAndPinErrorsKeepTheirCodes(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob, mallory := as(t, "acme", "alice"), as(t, "acme", "bob"), as(t, "acme", "mallory")
	rg.send(t, alice, room, "c-1", "hi")
	rg.send(t, alice, room, "c-2", "gone")
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	react := func(ctx context.Context, req *chatimv1.ReactMessageRequest) func() error {
		return func() error { _, err := rg.client.ReactMessage(ctx, req); return err }
	}
	pin := func(ctx context.Context, req *chatimv1.PinMessageRequest) func() error {
		return func() error { _, err := rg.client.PinMessage(ctx, req); return err }
	}
	unpin := func(ctx context.Context, req *chatimv1.UnpinMessageRequest) func() error {
		return func() error { _, err := rg.client.UnpinMessage(ctx, req); return err }
	}
	cases := []struct {
		name string
		call func() error
		code codes.Code
	}{
		{"control character emoji", react(bob, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "\u0007"}), codes.InvalidArgument},
		{"unknown message", react(bob, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 9, Emoji: "👍"}), codes.NotFound},
		{"stranger", react(mallory, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "👍"}), codes.PermissionDenied},
		{"other tenant", react(as(t, "other", "bob"), &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "👍"}), codes.NotFound},
		{"bad room id", react(bob, &chatimv1.ReactMessageRequest{RoomId: "x", Seq: 1, Emoji: "👍"}), codes.InvalidArgument},
		{"thread", react(bob, &chatimv1.ReactMessageRequest{RoomId: room, ThreadRoot: 1, Seq: 1, Emoji: "👍"}), codes.InvalidArgument},
		{"react on a deleted message", react(bob, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 2, Emoji: "👍"}), codes.FailedPrecondition},
		{"pin a deleted message", pin(bob, &chatimv1.PinMessageRequest{RoomId: room, Seq: 2}), codes.FailedPrecondition},
		{"pin an unknown message", pin(bob, &chatimv1.PinMessageRequest{RoomId: room, Seq: 9}), codes.NotFound},
		{"unpin by a stranger", unpin(mallory, &chatimv1.UnpinMessageRequest{RoomId: room, Seq: 1}), codes.PermissionDenied},
		{"unpin without a seq", unpin(bob, &chatimv1.UnpinMessageRequest{RoomId: room}), codes.InvalidArgument},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { expectCode(t, c.call(), c.code) })
	}
	if _, err := rg.client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("removing nothing from a deleted message = %v, want success", err)
	}
}
