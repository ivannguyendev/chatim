package grpcsrv_test

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestHistoryPagesThroughWhatWasSent(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	sends := []struct{ user, cid, text string }{{"alice", "c-1", "một"}, {"bob", "c-2", "hai"}, {"alice", "c-3", "ba"}}
	sent := make([]*chatimv1.Message, len(sends))
	for i, s := range sends {
		ack := rg.send(t, as(t, "acme", s.user), room, s.cid, s.text)
		if ack.GetSeq() != uint64(i+1) || ack.GetPts() != uint64(i+1) {
			t.Fatalf("send %d acked seq %d pts %d, want %d", i, ack.GetSeq(), ack.GetPts(), i+1)
		}
		sent[i] = &chatimv1.Message{
			RoomId: room, Seq: ack.GetSeq(), Pts: ack.GetPts(), Sender: s.user, Kind: chatimv1.MessageKind_MESSAGE_KIND_TEXT,
			Text: s.text, Cid: s.cid, CreatedAt: ack.GetCreatedAt(),
		}
	}
	const (
		latest = chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST
		oldest = chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST
		before = chatimv1.HistoryAnchor_HISTORY_ANCHOR_BEFORE
		after  = chatimv1.HistoryAnchor_HISTORY_ANCHOR_AFTER
	)
	cases := []struct {
		name   string
		anchor chatimv1.HistoryAnchor
		seq    uint64
		limit  int32
		want   []int
	}{
		{"unspecified is latest", chatimv1.HistoryAnchor_HISTORY_ANCHOR_UNSPECIFIED, 0, 0, []int{0, 1, 2}},
		{"latest two", latest, 0, 2, []int{1, 2}},
		{"latest ignores seq", latest, 1, 0, []int{0, 1, 2}},
		{"oldest two", oldest, 0, 2, []int{0, 1}},
		{"oldest ignores seq", oldest, 3, 1, []int{0}},
		{"before three", before, 3, 0, []int{0, 1}},
		{"before three, one", before, 3, 1, []int{1}},
		{"before one", before, 1, 0, nil},
		{"after one", after, 1, 0, []int{1, 2}},
		{"after one, one", after, 1, 1, []int{1}},
		{"after the end", after, 3, 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := rg.client.GetHistory(as(t, "acme", "bob"), &chatimv1.GetHistoryRequest{RoomId: room, Anchor: c.anchor, Seq: c.seq, Limit: c.limit})
			if err != nil {
				t.Fatalf("GetHistory: %v", err)
			}
			got := resp.GetMessages()
			if len(got) != len(c.want) {
				t.Fatalf("got %d messages %v, want indexes %v", len(got), got, c.want)
			}
			for i, idx := range c.want {
				if !proto.Equal(got[i], sent[idx]) {
					t.Errorf("message %d = %v, want %v", i, got[i], sent[idx])
				}
			}
		})
	}
}

func TestHistoryRejectsBadInput(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	cases := map[string]*chatimv1.GetHistoryRequest{
		"empty room id":       {},
		"room id not decimal": {RoomId: "4x2"},
		"room id zero":        {RoomId: "0"},
		"thread":              {RoomId: room, ThreadRoot: 1},
		"unknown anchor":      {RoomId: room, Anchor: chatimv1.HistoryAnchor(9)},
		"before without seq":  {RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_BEFORE},
		"after without seq":   {RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_AFTER},
		"negative limit":      {RoomId: room, Limit: -1},
		"limit over 100":      {RoomId: room, Limit: 101},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.GetHistory(as(t, "acme", "alice"), req)
			expectCode(t, err, codes.InvalidArgument)
		})
	}
	resp, err := rg.client.GetHistory(as(t, "acme", "alice"), &chatimv1.GetHistoryRequest{RoomId: room, Limit: 100})
	if err != nil || len(resp.GetMessages()) != 0 {
		t.Fatalf("limit 100 on an empty room = %v, %v; want no messages", resp, err)
	}
}

func TestHistoryHidesRoomsOfOtherTenants(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	rg.send(t, as(t, "acme", "alice"), room, "c-1", "secret")
	for name, id := range map[string]string{"other tenant": room, "unknown room": "4242"} {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.GetHistory(as(t, "other", "alice"), &chatimv1.GetHistoryRequest{RoomId: id})
			expectCode(t, err, codes.NotFound)
		})
	}
}

func TestHistoryRequiresMembership(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	rg.send(t, as(t, "acme", "alice"), room, "c-1", "secret")
	_, err := rg.client.GetHistory(as(t, "acme", "mallory"), &chatimv1.GetHistoryRequest{RoomId: room})
	expectCode(t, err, codes.PermissionDenied)
}
