package grpcsrv_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (rg *rig) reactAt(t *testing.T, clock *testClock, room, user, emoji string, seq uint64, at time.Duration) {
	t.Helper()
	clock.set(sentBase.Add(at))
	if _, err := rg.client.ReactMessage(as(t, "acme", user), &chatimv1.ReactMessageRequest{RoomId: room, Seq: seq, Emoji: emoji}); err != nil {
		t.Fatalf("%s ReactMessage(%s): %v", user, emoji, err)
	}
}

func reactorsSeen(t *testing.T, rg *rig, ctx context.Context, room, emoji, after string, limit uint32) ([]string, string) {
	t.Helper()
	resp, err := rg.client.GetReactions(ctx, &chatimv1.GetReactionsRequest{RoomId: room, Seq: 1, Emoji: emoji, After: after, Limit: limit})
	if err != nil {
		t.Fatalf("GetReactions(%q after %q): %v", emoji, after, err)
	}
	var out []string
	for _, r := range resp.GetReactors() {
		out = append(out, r.GetUser()+" "+r.GetEmoji()+" "+r.GetAt().AsTime().Format("05"))
	}
	return out, resp.GetNext()
}

func reactedRoom(t *testing.T) (*rig, *testClock, string) {
	t.Helper()
	clock := &testClock{now: sentBase}
	rg := newRig(t, options{now: clock.Now})
	room := rg.createGroup(t, "acme", "alice", "bob", "carol", "dan")
	rg.insertAt(t, room, 1, "hi", sentAtSeq(1))
	rg.insertAt(t, room, 2, "other", sentAtSeq(2))
	rg.reactAt(t, clock, room, "alice", "👍", 1, time.Second)
	rg.reactAt(t, clock, room, "carol", "❤️", 1, 2*time.Second)
	rg.reactAt(t, clock, room, "dan", "👍", 1, 3*time.Second)
	rg.reactAt(t, clock, room, "bob", "👍", 1, 3*time.Second)
	rg.reactAt(t, clock, room, "bob", "👍", 2, 4*time.Second)
	return rg, clock, room
}

func TestGetReactionsListsOneEmojiOrAllThroughTheCursor(t *testing.T) {
	rg, _, room := reactedRoom(t)
	alice := as(t, "acme", "alice")
	thumbs, next := reactorsSeen(t, rg, alice, room, "👍", "", 2)
	if want := []string{"bob 👍 03", "dan 👍 03"}; !slices.Equal(thumbs, want) || next == "" {
		t.Fatalf("👍 page = %v next %q, want %v and a cursor", thumbs, next, want)
	}
	rest, next := reactorsSeen(t, rg, alice, room, "👍", next, 2)
	if want := []string{"alice 👍 01"}; !slices.Equal(rest, want) || next != "" {
		t.Fatalf("👍 rest = %v next %q, want %v and no cursor", rest, next, want)
	}
	var all []string
	after := ""
	for {
		page, next := reactorsSeen(t, rg, alice, room, "", after, 1)
		all = append(all, page...)
		if next == "" {
			break
		}
		after = next
	}
	if want := []string{"carol ❤️ 02", "bob 👍 03", "dan 👍 03", "alice 👍 01"}; !slices.Equal(all, want) {
		t.Fatalf("all tab = %v, want %v", all, want)
	}
}

func TestGetReactionsIsEmptyForADeletedOrHiddenMessage(t *testing.T) {
	rg, _, room := reactedRoom(t)
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 1}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	if got, next := reactorsSeen(t, rg, bob, room, "", "", 0); len(got) != 0 || next != "" {
		t.Fatalf("bob sees %v on a hidden message, want nothing", got)
	}
	if got, _ := reactorsSeen(t, rg, alice, room, "", "", 0); len(got) != 4 {
		t.Fatalf("alice sees %v, want 4 reactors", got)
	}
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if got, _ := reactorsSeen(t, rg, alice, room, "👍", "", 0); len(got) != 0 {
		t.Fatalf("alice sees %v on a deleted message, want nothing", got)
	}
}

func TestGetReactionsCodes(t *testing.T) {
	rg, _, room := reactedRoom(t)
	cases := map[string]struct {
		user string
		req  *chatimv1.GetReactionsRequest
		code codes.Code
	}{
		"emoji outside the list before membership": {"zoe", &chatimv1.GetReactionsRequest{RoomId: room, Seq: 1, Emoji: "🦄"}, codes.InvalidArgument},
		"non-member":      {"zoe", &chatimv1.GetReactionsRequest{RoomId: room, Seq: 1}, codes.PermissionDenied},
		"missing message": {"bob", &chatimv1.GetReactionsRequest{RoomId: room, Seq: 9}, codes.NotFound},
		"zero seq":        {"bob", &chatimv1.GetReactionsRequest{RoomId: room}, codes.InvalidArgument},
		"thread root":     {"bob", &chatimv1.GetReactionsRequest{RoomId: room, ThreadRoot: 3, Seq: 1}, codes.InvalidArgument},
		"bad cursor":      {"bob", &chatimv1.GetReactionsRequest{RoomId: room, Seq: 1, After: "x"}, codes.InvalidArgument},
		"limit too big":   {"bob", &chatimv1.GetReactionsRequest{RoomId: room, Seq: 1, Limit: 101}, codes.InvalidArgument},
	}
	for name, c := range cases {
		_, err := rg.client.GetReactions(as(t, "acme", c.user), c.req)
		t.Run(name, func(t *testing.T) { expectCode(t, err, c.code) })
	}
}
