package grpcsrv_test

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type threadSeen struct {
	parent  uint64
	hidden  bool
	lastAt  int64
	preview string
}

func (rg *rig) thread(t *testing.T, room string, parent, lastReply uint64, replies int) {
	t.Helper()
	key := store.MsgKey{Room: roomNumber(t, room), Seq: parent}
	if replies > 0 {
		if _, err := rg.msgs.AddReplyCount(t.Context(), key, replies); err != nil {
			t.Fatalf("AddReplyCount(%d): %v", parent, err)
		}
	}
	if err := rg.threads.Touch(t.Context(), key, "acme", lastReply, sentAtSeq(lastReply)); err != nil {
		t.Fatalf("Touch(%d): %v", parent, err)
	}
}

func threadsSeen(t *testing.T, rg *rig, ctx context.Context, room, before string, limit uint32) ([]threadSeen, string) {
	t.Helper()
	resp, err := rg.client.ListThreads(ctx, &chatimv1.ListThreadsRequest{RoomId: room, Before: before, Limit: limit})
	if err != nil {
		t.Fatalf("ListThreads(before %q): %v", before, err)
	}
	var out []threadSeen
	for _, it := range resp.GetItems() {
		out = append(out, threadSeen{it.GetMessage().GetSeq(), it.GetMessage().GetHidden(), it.GetLastReplyAt().AsTime().Unix(), it.GetLastReplyPreview().GetText()})
	}
	return out, resp.GetNext()
}

func TestListThreadsPagesNewestReplyFirstAndDropsEmptyThreads(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	bob := as(t, "acme", "bob")
	for seq := uint64(1); seq <= 4; seq++ {
		rg.insertAt(t, room, seq, "p", sentAtSeq(seq))
	}
	for reply, parent := range map[uint64]uint64{5: 1, 6: 2, 7: 3, 8: 4, 9: 1} {
		rg.insertReply(t, room, reply, parent)
	}
	rg.thread(t, room, 1, 9, 2)
	rg.thread(t, room, 2, 6, 1)
	rg.thread(t, room, 3, 7, 0)
	rg.thread(t, room, 4, 8, 1)
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 4}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	at := func(seq uint64) int64 { return sentAtSeq(seq).Unix() }
	first, next := threadsSeen(t, rg, bob, room, "", 2)
	if want := []threadSeen{{1, false, at(9), "r9"}, {4, true, at(8), "r8"}}; !slices.Equal(first, want) || next == "" {
		t.Fatalf("first page = %+v next %q, want %+v and a cursor", first, next, want)
	}
	second, next := threadsSeen(t, rg, bob, room, next, 2)
	if want := []threadSeen{{2, false, at(6), "r6"}}; !slices.Equal(second, want) || next == "" {
		t.Fatalf("second page = %+v next %q, want %+v short with a cursor", second, next, want)
	}
	if last, next := threadsSeen(t, rg, bob, room, next, 2); len(last) != 0 || next != "" {
		t.Fatalf("last page = %+v next %q, want empty and no cursor", last, next)
	}
	all, _ := threadsSeen(t, rg, as(t, "acme", "alice"), room, "", 0)
	if len(all) != 3 || all[1].hidden {
		t.Fatalf("alice threads = %+v, want 3 with nothing hidden", all)
	}
}

func TestListThreadsCodes(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	cases := map[string]struct {
		ctx  context.Context
		req  *chatimv1.ListThreadsRequest
		code codes.Code
	}{
		"non-member":      {as(t, "acme", "carol"), &chatimv1.ListThreadsRequest{RoomId: room}, codes.PermissionDenied},
		"bad cursor":      {as(t, "acme", "bob"), &chatimv1.ListThreadsRequest{RoomId: room, Before: "x"}, codes.InvalidArgument},
		"limit too big":   {as(t, "acme", "bob"), &chatimv1.ListThreadsRequest{RoomId: room, Limit: 101}, codes.InvalidArgument},
		"missing room id": {as(t, "acme", "bob"), &chatimv1.ListThreadsRequest{RoomId: "x"}, codes.InvalidArgument},
	}
	for name, c := range cases {
		_, err := rg.client.ListThreads(c.ctx, c.req)
		t.Run(name, func(t *testing.T) { expectCode(t, err, c.code) })
	}
}
