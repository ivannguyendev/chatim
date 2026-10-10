package itest

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func sendLinked(t *testing.T, client chatimv1.CoreServiceClient, user string, req *chatimv1.SendMessageRequest) uint64 {
	t.Helper()
	resp, err := sendRetrying(callerAs(t.Context(), user), client, req)
	if err != nil {
		t.Fatalf("SendMessage(%s as %s): %v", req.GetCid(), user, err)
	}
	return resp.GetSeq()
}

func seqsOf(msgs []*chatimv1.Message) []uint64 {
	out := make([]uint64, len(msgs))
	for i, m := range msgs {
		out[i] = m.GetSeq()
	}
	return out
}

func repliesOf(t *testing.T, client chatimv1.CoreServiceClient, roomID string, parent uint64) []*chatimv1.Message {
	t.Helper()
	resp, err := client.GetReplies(caller(t.Context()), &chatimv1.GetRepliesRequest{RoomId: roomID, Seq: parent, Limit: 10})
	if err != nil {
		t.Fatalf("GetReplies(%d): %v", parent, err)
	}
	return resp.GetMessages()
}

func deleteAs(ctx context.Context, client chatimv1.CoreServiceClient, user, roomID string, seq uint64) error {
	_, err := retryingUnavailable(callerAs(ctx, user), func(ctx context.Context) (*chatimv1.DeleteMessageResponse, error) {
		return client.DeleteMessage(ctx, &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: seq})
	})
	return err
}

func awaitReplyCount(t *testing.T, live <-chan *nats.Msg, room, parent, ver uint64, want uint32) {
	t.Helper()
	id := pbconv.MessageCountsEventID(room, 0, parent, pbconv.RepliesCounter, ver)
	if got := awaitLiveEvents(t, live, id)[id].GetCountsChanged().GetReplyCount(); got.GetCount() != want || got.GetVer() != ver {
		t.Fatalf("event %s counts %v, want %d replies at version %d", id, got, want, ver)
	}
}

func TestRealInfraRepliesAreCountedListedAndKeepTheParent(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoomWith(t, client, []string{itUser, "bob"})
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)

	parent := sendAs(t, client, itUser, roomID, "parent", "the parent")
	var replies []uint64
	for _, cid := range []string{"reply-1", "reply-2"} {
		replies = append(replies, sendLinked(t, client, "bob", &chatimv1.SendMessageRequest{
			RoomId: roomID, Cid: cid, Text: "answer " + cid, ReplyTo: &chatimv1.ReplyRef{Seq: parent},
		}))
	}
	awaitReplyCount(t, live, room, parent, 2, 2)
	page := historyAs(t, client, itUser, roomID)
	if got := page[parent].GetReplyCount(); got.GetCount() != 2 {
		t.Fatalf("parent counts %v in history, want 2 replies", got)
	}
	if got := page[replies[0]]; got.GetReplyTo().GetSeq() != parent || got.GetReplyPreview().GetSeq() != parent || got.GetReplyPreview().GetText() != "the parent" {
		t.Fatalf("reply %d = %v, want a reply to seq %d with its preview", replies[0], got, parent)
	}
	if got := seqsOf(repliesOf(t, client, roomID, parent)); !slices.Equal(got, replies) {
		t.Fatalf("GetReplies = %v, want %v", got, replies)
	}

	if err := deleteAs(t.Context(), client, itUser, roomID, parent); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("delete of the replied parent = %v, want FailedPrecondition", err)
	}
	if err := deleteAs(t.Context(), client, "bob", roomID, replies[1]); err != nil {
		t.Fatalf("delete reply %d: %v", replies[1], err)
	}
	awaitReplyCount(t, live, room, parent, 3, 1)
	if got := seqsOf(repliesOf(t, client, roomID, parent)); !slices.Equal(got, replies[:1]) {
		t.Fatalf("GetReplies after a delete = %v, want %v", got, replies[:1])
	}
	if err := deleteAs(t.Context(), client, itUser, roomID, parent); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("delete of the parent with one reply left = %v, want FailedPrecondition", err)
	}
}

func TestRealInfraMentionsForwardsAndBookmarksReachTheirLists(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID, otherID := createRoomWith(t, client, []string{itUser, "bob"}), createRoomWith(t, client, []string{itUser, "bob"})
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)

	since := time.Now().Add(-time.Minute)
	sets := []*chatimv1.MentionSet{
		{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "bob"}}},
		{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "design"}}},
		{All: true},
		{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "carol"}}},
	}
	var seqs []uint64
	for i, set := range sets {
		seqs = append(seqs, sendLinked(t, client, itUser, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "mention-" + strconv.Itoa(i), Text: "mention", Mentions: set}))
	}
	want := []uint64{seqs[2], seqs[1], seqs[0]}
	req := &chatimv1.ListMentionsRequest{Groups: []*chatimv1.MentionGroup{{Id: "design", Since: timestamppb.New(since)}}, Limit: 10}
	awaitStored(t, "mentions of bob", func() ([]uint64, error) {
		resp, err := client.ListMentions(callerAs(t.Context(), "bob"), req)
		return seqsOf(resp.GetMessages()), err
	}, func(got []uint64) bool { return slices.Equal(got, want) })

	fwd := sendLinked(t, client, "bob", &chatimv1.SendMessageRequest{RoomId: otherID, Cid: "forward-1", ForwardFrom: &chatimv1.ForwardRef{RoomId: roomID, Seq: seqs[0]}})
	got := historyAs(t, client, "bob", otherID)[fwd]
	if f := got.GetForwardFrom(); f.GetRoomId() != roomID || f.GetSeq() != seqs[0] || f.GetAuthor() != itUser || got.GetText() != "mention" || got.GetSender() != "bob" {
		t.Fatalf("forwarded message = %v, want seq %d of room %s by %s with its text", got, seqs[0], roomID, itUser)
	}

	resp, err := client.SetBookmark(callerAs(t.Context(), "bob"), &chatimv1.SetBookmarkRequest{RoomId: roomID, Seq: seqs[0], On: true})
	if err != nil || !resp.GetChanged() {
		t.Fatalf("SetBookmark = %v, %v; want a change", resp, err)
	}
	awaitLiveEvents(t, live, pbconv.BookmarkEventID(room, 0, seqs[0], "bob", 1))
	marks, err := client.ListBookmarks(callerAs(t.Context(), "bob"), &chatimv1.ListBookmarksRequest{Limit: 10})
	items := marks.GetItems()
	if err != nil || len(items) != 1 || !items[0].GetAvailable() || items[0].GetMessage().GetRoomId() != roomID || items[0].GetMessage().GetSeq() != seqs[0] {
		t.Fatalf("ListBookmarks = %v, %v; want seq %d of room %s available", items, err, seqs[0], roomID)
	}
}
