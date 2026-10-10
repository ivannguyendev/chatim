package e2e_test

import (
	"strings"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func TestLinkEventIDsFollowTheCore(t *testing.T) {
	cases := map[string]string{
		e2e.RepliesEventID("77", 1, 2):                       "77-0-1-replies-v2",
		e2e.BookmarkEventID("77", 4, "e2e-p", 1):             "77-bm-0-4-e2e-p-v1",
		e2e.LiveSubject("live", "t", "77", e2e.KindBookmark): "live.t.member.77.evt.bookmark_changed",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestLinksPayloadNamesEveryLinkOfAMessage(t *testing.T) {
	m := &chatimv1.Message{
		ReplyTo:        &chatimv1.ReplyRef{Seq: 1},
		ForwardFrom:    &chatimv1.ForwardRef{RoomId: "88", Seq: 2, Author: "bob"},
		MentionTargets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "p"}, {Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "g"}},
		MentionAll:     true,
	}
	want := "reply_to=1 forward_from=88-0-2 author=bob mentions=user:p,group:g all"
	if got := e2e.LinksPayload(m); got != want {
		t.Fatalf("LinksPayload = %q, want %q", got, want)
	}
	if got := e2e.LinksPayload(&chatimv1.Message{Text: "plain"}); got != "" {
		t.Fatalf("LinksPayload of a plain message = %q, want empty", got)
	}
}

func TestEventOfCarriesLinksRepliesAndBookmarks(t *testing.T) {
	created := &chatimv1.Event{Id: "77-0-2", RoomId: "77", Seq: 2, Payload: &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{
		Message: &chatimv1.Message{Seq: 2, Cid: "c2", ReplyTo: &chatimv1.ReplyRef{Seq: 1}},
	}}}
	replies := &chatimv1.Event{Id: "77-0-1-replies-v2", RoomId: "77", Seq: 1, Payload: &chatimv1.Event_CountsChanged{CountsChanged: &chatimv1.CountsChanged{
		Counter: "replies", ReplyCount: &chatimv1.ReplyCount{Count: 2, Ver: 2},
	}}}
	bookmark := &chatimv1.Event{Id: "77-bm-0-4-p-v1", RoomId: "77", Seq: 4, Payload: &chatimv1.Event_BookmarkChanged{BookmarkChanged: &chatimv1.BookmarkChanged{
		Seq: 4, User: "p", On: true, Ver: 1,
	}}}
	cases := []struct {
		ev   *chatimv1.Event
		want e2e.Event
	}{
		{created, e2e.Event{Kind: e2e.KindCreated, Room: "77", ID: "77-0-2", Seq: 2, CID: "c2", Text: "reply_to=1", Subject: "s"}},
		{replies, e2e.Event{Kind: e2e.KindCounts, Room: "77", ID: "77-0-1-replies-v2", Seq: 1, Text: e2e.RepliesPayload(2), Subject: "s"}},
		{bookmark, e2e.Event{Kind: e2e.KindBookmark, Room: "77", ID: "77-bm-0-4-p-v1", Seq: 4, User: "p", Version: 1, Text: e2e.BookmarkPayload(true), Subject: "s"}},
	}
	for _, c := range cases {
		got, ok := e2e.EventOf("s", c.ev)
		if !ok || got != c.want {
			t.Fatalf("EventOf(%s) = %+v, %v; want %+v", c.ev.GetId(), got, ok, c.want)
		}
	}
	if got, _ := e2e.EventOf("s", bookmark); !got.IsMember() {
		t.Fatalf("bookmark event %+v is not a member event", got)
	}
}

func TestCheckSeqsWantsTheExactRoomAndOrder(t *testing.T) {
	got := []*chatimv1.Message{{RoomId: "77", Seq: 6}, {RoomId: "77", Seq: 5}}
	if err := e2e.CheckSeqs("mentions", "77", got, []uint64{6, 5}); err != nil {
		t.Fatalf("CheckSeqs: %v", err)
	}
	for _, want := range [][]uint64{{5, 6}, {6}, {6, 5, 4}} {
		if err := e2e.CheckSeqs("mentions", "77", got, want); err == nil {
			t.Fatalf("CheckSeqs(%v) passed for seqs 6,5", want)
		}
	}
	if err := e2e.CheckSeqs("mentions", "88", got, []uint64{6, 5}); err == nil || !strings.Contains(err.Error(), "room 77") {
		t.Fatalf("CheckSeqs in another room = %v, want a room error", err)
	}
}

func TestCheckRepliesWantsEachReplyToPointAtTheParent(t *testing.T) {
	good := []*chatimv1.Message{
		{RoomId: "77", Seq: 2, ReplyTo: &chatimv1.ReplyRef{Seq: 1}},
		{RoomId: "77", Seq: 3, ReplyTo: &chatimv1.ReplyRef{Seq: 1}},
	}
	if err := e2e.CheckReplies("77", 1, good, []uint64{2, 3}); err != nil {
		t.Fatalf("CheckReplies: %v", err)
	}
	bad := []*chatimv1.Message{good[0], {RoomId: "77", Seq: 3, ReplyTo: &chatimv1.ReplyRef{Seq: 2}}}
	if err := e2e.CheckReplies("77", 1, bad, []uint64{2, 3}); err == nil {
		t.Fatal("CheckReplies passed a reply to another parent")
	}
}

func TestCheckReplyCountReadsTheCountOfTheParent(t *testing.T) {
	m := &chatimv1.Message{Seq: 1, ReplyCount: &chatimv1.ReplyCount{Count: 2, Ver: 2}}
	if err := e2e.CheckReplyCount(m, 2); err != nil {
		t.Fatalf("CheckReplyCount: %v", err)
	}
	if err := e2e.CheckReplyCount(m, 1); err == nil {
		t.Fatal("CheckReplyCount passed a count of 2 for a want of 1")
	}
}

func TestCheckBookmarksWantsAvailableItemsInOrder(t *testing.T) {
	items := []*chatimv1.BookmarkItem{{Message: &chatimv1.Message{RoomId: "77", Seq: 4, Text: "x"}, Available: true}}
	if err := e2e.CheckBookmarks("77", items, []uint64{4}); err != nil {
		t.Fatalf("CheckBookmarks: %v", err)
	}
	items[0].Available = false
	if err := e2e.CheckBookmarks("77", items, []uint64{4}); err == nil {
		t.Fatal("CheckBookmarks passed an unavailable item")
	}
}

func TestCheckForwardWantsTheOriginAndItsText(t *testing.T) {
	origin := e2e.Origin{Room: "88", Seq: 2, Author: "bob", Text: "hi"}
	m := &chatimv1.Message{Text: "hi", ForwardFrom: &chatimv1.ForwardRef{RoomId: "88", Seq: 2, Author: "bob"}}
	if err := e2e.CheckForward(m, origin); err != nil {
		t.Fatalf("CheckForward: %v", err)
	}
	m.Text = "changed"
	if err := e2e.CheckForward(m, origin); err == nil {
		t.Fatal("CheckForward passed a forward with another text")
	}
}
