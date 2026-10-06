package main

import (
	"maps"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraMembersReactAndPinEvenOnLockedKinds(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastEffects)
	env["MESSAGE_LOCKED_KINDS"] = "text"
	core := startCore(t, it, env)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	seq := sendAs(t, client, itUser, roomID, "access-e", "locked text")
	edit := &chatimv1.EditMessageRequest{RoomId: roomID, Seq: seq, Text: "not allowed"}
	if _, err := client.EditMessage(caller(t.Context()), edit); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("the author edits a locked kind = %v, want PermissionDenied (MESSAGE_LOCKED_KINDS active)", err)
	}

	bob := callerAs(t.Context(), "bob")
	if resp, err := client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq, Emoji: "👍"}); err != nil || resp.GetChange() != 1 {
		t.Fatalf("bob reacts to alice's locked message = %v, %v; want change 1", resp, err)
	}
	if resp, err := client.PinMessage(bob, &chatimv1.PinMessageRequest{RoomId: roomID, Seq: seq}); err != nil || resp.GetPinVersion() != 1 {
		t.Fatalf("bob pins alice's locked message = %v, %v; want pin version 1", resp, err)
	}
	if resp, err := client.UnpinMessage(bob, &chatimv1.UnpinMessageRequest{RoomId: roomID, Seq: seq}); err != nil || resp.GetPinVersion() != 2 || len(resp.GetPins()) != 0 {
		t.Fatalf("bob unpins it = %v, %v; want pin version 2 and no pins", resp, err)
	}

	carol := callerAs(t.Context(), "carol")
	denied := map[string]func() error{
		"react": func() error {
			_, err := client.ReactMessage(carol, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq, Emoji: "👍"})
			return err
		},
		"remove": func() error {
			_, err := client.ReactMessage(carol, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq})
			return err
		},
		"pin": func() error {
			_, err := client.PinMessage(carol, &chatimv1.PinMessageRequest{RoomId: roomID, Seq: seq})
			return err
		},
		"unpin": func() error {
			_, err := client.UnpinMessage(carol, &chatimv1.UnpinMessageRequest{RoomId: roomID, Seq: seq})
			return err
		},
	}
	for name, call := range denied {
		if err := call(); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("carol (not a member) %s = %v, want PermissionDenied", name, err)
		}
	}
}

func TestRealInfraDeletedMessagesTakeNoNewReactionOrPin(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	seq := sendAs(t, client, itUser, roomID, "deleted-f", "soon deleted")
	bob := callerAs(t.Context(), "bob")
	if _, err := client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq, Emoji: "👍"}); err != nil {
		t.Fatalf("ReactMessage: %v", err)
	}
	if _, err := client.PinMessage(caller(t.Context()), &chatimv1.PinMessageRequest{RoomId: roomID, Seq: seq}); err != nil {
		t.Fatalf("PinMessage: %v", err)
	}
	if _, err := client.DeleteMessage(caller(t.Context()), &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: seq}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	if _, err := client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq, Emoji: "❤️"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("react on a deleted message = %v, want FailedPrecondition", err)
	}
	if _, err := client.PinMessage(caller(t.Context()), &chatimv1.PinMessageRequest{RoomId: roomID, Seq: seq}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("pin of a deleted message = %v, want FailedPrecondition even while it is pinned", err)
	}
	removed, err := client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq})
	if err != nil || removed.GetChange() != 2 || len(removed.GetReactions().GetCounts()) != 0 {
		t.Fatalf("remove on a deleted message = %v, %v; want change 2 and no counts", removed, err)
	}
	unpinned, err := client.UnpinMessage(caller(t.Context()), &chatimv1.UnpinMessageRequest{RoomId: roomID, Seq: seq})
	if err != nil || unpinned.GetPinVersion() != 2 || len(unpinned.GetPins()) != 0 {
		t.Fatalf("unpin of a deleted message = %v, %v; want pin version 2 and no pins", unpinned, err)
	}
	if m := historyAs(t, client, "bob", roomID)[seq]; !m.GetDeleted() || m.GetText() != "" || m.GetReactions() != nil {
		t.Fatalf("history shows %v, want a deleted placeholder without reactions", m)
	}
}
