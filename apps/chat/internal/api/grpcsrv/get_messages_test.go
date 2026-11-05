package grpcsrv_test

import (
	"math"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestGetMessagesReturnsKnownSeqsAscending(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	for seq := uint64(1); seq <= 4; seq++ {
		rg.insertAt(t, room, seq, "m", sentAtSeq(seq))
	}
	rg.insertReply(t, room, 5, 2)
	bob := as(t, "acme", "bob")
	resp, err := rg.client.GetMessages(bob, &chatimv1.GetMessagesRequest{RoomId: room, Seqs: []uint64{5, 4, 2, 2, 9}})
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if got := seqsOf(resp.GetMessages()); !slices.Equal(got, []uint64{2, 4, 5}) {
		t.Fatalf("GetMessages seqs = %v, want [2 4 5]", got)
	}
	if p := resp.GetMessages()[2].GetReplyPreview(); p.GetSeq() != 2 {
		t.Fatalf("reply preview of seq 5 = %v, want the parent seq 2", p)
	}
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 4}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	resp, err = rg.client.GetMessages(bob, &chatimv1.GetMessagesRequest{RoomId: room, Seqs: []uint64{4}})
	if err != nil || len(resp.GetMessages()) != 1 || !resp.GetMessages()[0].GetHidden() || resp.GetMessages()[0].GetText() != "" {
		t.Fatalf("GetMessages of a hidden seq = %v, %v; want one hidden placeholder", resp, err)
	}
}

func TestGetMessagesRejectsBadInputAndChecksAccess(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	rg.insertAt(t, room, 1, "m", sentAtSeq(1))
	tooMany := make([]uint64, 101)
	for i := range tooMany {
		tooMany[i] = uint64(i + 1)
	}
	cases := map[string]*chatimv1.GetMessagesRequest{
		"no room":      {Seqs: []uint64{1}},
		"no seqs":      {RoomId: room},
		"over 100":     {RoomId: room, Seqs: tooMany},
		"seq zero":     {RoomId: room, Seqs: []uint64{1, 0}},
		"reserved seq": {RoomId: room, Seqs: []uint64{math.MaxUint64}},
		"thread":       {RoomId: room, ThreadRoot: 1, Seqs: []uint64{1}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.GetMessages(as(t, "acme", "alice"), req)
			expectCode(t, err, codes.InvalidArgument)
		})
	}
	resp, err := rg.client.GetMessages(as(t, "acme", "alice"), &chatimv1.GetMessagesRequest{RoomId: room, Seqs: tooMany[:100]})
	if err != nil || len(resp.GetMessages()) != 1 {
		t.Fatalf("GetMessages of 100 seqs = %v, %v; want the one stored message", resp, err)
	}
	_, err = rg.client.GetMessages(as(t, "acme", "mallory"), &chatimv1.GetMessagesRequest{RoomId: room, Seqs: []uint64{1}})
	expectCode(t, err, codes.PermissionDenied)
	for _, c := range []struct{ name, tenant, room string }{{"other tenant", "other", room}, {"unknown room", "acme", "4242"}} {
		t.Run(c.name, func(t *testing.T) {
			_, err := rg.client.GetMessages(as(t, c.tenant, "alice"), &chatimv1.GetMessagesRequest{RoomId: c.room, Seqs: []uint64{1}})
			expectCode(t, err, codes.NotFound)
		})
	}
}
