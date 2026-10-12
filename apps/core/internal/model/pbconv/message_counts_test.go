package pbconv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestOnlyReactionsAndRepliesAreMessageCounters(t *testing.T) {
	for name, want := range map[string]bool{"reactions": true, "replies": true, "": false, "members": false, "Replies": false, "reaction": false} {
		if got := pbconv.MessageCounter(name); got != want {
			t.Errorf("MessageCounter(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMessageCountsEventIDNamesTheCounter(t *testing.T) {
	if got := pbconv.MessageCountsEventID(42, 3, 9, pbconv.RepliesCounter, 5); got != "42-3-9-replies-v5" {
		t.Fatalf("replies id = %q, want 42-3-9-replies-v5", got)
	}
	if got, want := pbconv.ReactionCountsEventID(42, 0, 7, 2), pbconv.MessageCountsEventID(42, 0, 7, pbconv.ReactionsCounter, 2); got != want || got != "42-0-7-reactions-v2" {
		t.Fatalf("reactions id = %q, want %q", got, want)
	}
}

func TestReplyCountsChangedEnvelope(t *testing.T) {
	m := sample()
	m.Thread, m.Replies = 3, domain.ReplyCount{N: 0, Version: 4}
	want := &chatimv1.Event{
		Id: "9007199254740993-3-7-replies-v4", Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		ThreadRoot: 3, Seq: 7, Ts: timestamppb.New(reactedAt),
		Payload: &chatimv1.Event_CountsChanged{CountsChanged: &chatimv1.CountsChanged{Counter: pbconv.RepliesCounter, ReplyCount: &chatimv1.ReplyCount{Count: 0, Ver: 4}}},
	}
	if got := pbconv.ReplyCountsChanged(domain.RoomGroup, m, reactedAt); !proto.Equal(got, want) {
		t.Fatalf("ReplyCountsChanged = %v, want %v", got, want)
	}
}
