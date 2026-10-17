package grpcsrv_test

import (
	"math"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func seqsOf(msgs []*chatimv1.Message) []uint64 {
	out := []uint64{}
	for _, m := range msgs {
		out = append(out, m.GetSeq())
	}
	return out
}

func TestHistoryAroundSplitsBackAndAheadWithoutTopUp(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	for seq := uint64(1); seq <= 5; seq++ {
		rg.insertAt(t, room, seq, "m", sentAtSeq(seq))
	}
	cases := []struct {
		name  string
		seq   uint64
		limit int32
		want  []uint64
	}{
		{"default limit holds the whole room", 3, 0, []uint64{1, 2, 3, 4, 5}},
		{"even limit", 3, 2, []uint64{3, 4}},
		{"odd limit gives the back part one more", 3, 3, []uint64{2, 3, 4}},
		{"limit one is the anchor only", 3, 1, []uint64{3}},
		{"start of the timeline stays short", 1, 4, []uint64{1, 2, 3}},
		{"end of the timeline stays short", 5, 4, []uint64{4, 5}},
		{"past the end", 9, 4, []uint64{4, 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := rg.client.GetHistory(as(t, "acme", "bob"), &chatimv1.GetHistoryRequest{
				RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_AROUND, Seq: c.seq, Limit: c.limit,
			})
			if err != nil {
				t.Fatalf("GetHistory: %v", err)
			}
			if got := seqsOf(resp.GetMessages()); !slices.Equal(got, c.want) {
				t.Fatalf("AROUND %d limit %d = %v, want %v", c.seq, c.limit, got, c.want)
			}
		})
	}
}

func TestHistoryAroundRejectsBadSeqAndKeepsAccessRules(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	rg.insertAt(t, room, 1, "m", sentAtSeq(1))
	around := chatimv1.HistoryAnchor_HISTORY_ANCHOR_AROUND
	for name, seq := range map[string]uint64{"seq zero": 0, "reserved seq": math.MaxUint64} {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.GetHistory(as(t, "acme", "alice"), &chatimv1.GetHistoryRequest{RoomId: room, Anchor: around, Seq: seq})
			expectCode(t, err, codes.InvalidArgument)
		})
	}
	_, err := rg.client.GetHistory(as(t, "acme", "mallory"), &chatimv1.GetHistoryRequest{RoomId: room, Anchor: around, Seq: 1})
	expectCode(t, err, codes.PermissionDenied)
}
