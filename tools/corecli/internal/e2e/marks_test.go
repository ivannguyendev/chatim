package e2e_test

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const reactor = "e2e-user"

func summary(version uint64, emoji string) *chatimv1.ReactionSummary {
	return &chatimv1.ReactionSummary{Counts: []*chatimv1.ReactionCount{{Emoji: emoji, Count: 1}}, Ver: version}
}

func TestMarkEventIDsMatchTheCore(t *testing.T) {
	cases := map[string]string{
		e2e.ReactionEventID("42", 3, reactor, 2): "42-0-3-e2e-user-n2",
		e2e.CountsEventID("42", 3, 2):            "42-0-3-reactions-v2",
		e2e.PinEventID("42", 1):                  "42-p1",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("event id = %q, want %q", got, want)
		}
	}
}

func TestEventOfReadsReactionCountAndPinEvents(t *testing.T) {
	msg := &chatimv1.Message{RoomId: "42", Seq: 4, Cid: "a-d"}
	cases := []struct {
		ev   *chatimv1.Event
		want e2e.Event
	}{
		{
			&chatimv1.Event{Id: "42-0-3-e2e-user-n2", RoomId: "42", Seq: 3, Payload: &chatimv1.Event_ReactionChanged{
				ReactionChanged: &chatimv1.ReactionChanged{User: reactor, Emoji: "❤️", PreviousEmoji: "👍", Change: 2},
			}},
			e2e.Event{Kind: e2e.KindReaction, Room: "42", ID: "42-0-3-e2e-user-n2", Seq: 3, User: reactor, Version: 2, Text: "❤️", Subject: "s"},
		},
		{
			&chatimv1.Event{Id: "42-0-3-reactions-v2", RoomId: "42", Seq: 3, Payload: &chatimv1.Event_CountsChanged{
				CountsChanged: &chatimv1.CountsChanged{Counter: "reactions", Reactions: summary(2, "❤️")},
			}},
			e2e.Event{Kind: e2e.KindCounts, Room: "42", ID: "42-0-3-reactions-v2", Seq: 3, Text: "❤️=1", Subject: "s"},
		},
		{
			&chatimv1.Event{Id: "42-p1", RoomId: "42", Seq: 4, Payload: &chatimv1.Event_MessagePinned{
				MessagePinned: &chatimv1.MessagePinned{Message: msg, PinVer: 1},
			}},
			e2e.Event{Kind: e2e.KindPinned, Room: "42", ID: "42-p1", Seq: 4, CID: "a-d", Subject: "s"},
		},
		{
			&chatimv1.Event{Id: "42-p2", RoomId: "42", Seq: 4, Payload: &chatimv1.Event_MessageUnpinned{
				MessageUnpinned: &chatimv1.MessageUnpinned{Message: msg, PinVer: 2},
			}},
			e2e.Event{Kind: e2e.KindUnpinned, Room: "42", ID: "42-p2", Seq: 4, CID: "a-d", Subject: "s"},
		},
	}
	for _, c := range cases {
		got, ok := e2e.EventOf("s", c.ev)
		if !ok || got != c.want || !got.IsMark() || got.IsChange() || got.IsCreated() {
			t.Fatalf("EventOf(%s) = %+v, %v; want %+v, true", c.ev.GetId(), got, ok, c.want)
		}
	}
}

func TestCheckEventsSkipsReactionAndPinEvents(t *testing.T) {
	as := acks(1)
	created := e2e.Event{Kind: e2e.KindCreated, Room: room, ID: e2e.MessageEventID(room, 1), Seq: 1, CID: as[0].CID}
	counts := e2e.Event{Kind: e2e.KindCounts, Room: room, ID: e2e.CountsEventID(room, 9, 1), Seq: 9}
	if cov, err := e2e.CheckEvents(as, room, []e2e.Event{created, counts}); err != nil || cov.Distinct != 1 || cov.MissingCount != 0 {
		t.Fatalf("CheckEvents = %+v, %v; want the counts event skipped", cov, err)
	}
}

func TestCheckReactAndPinReplies(t *testing.T) {
	want := e2e.Reaction{Seq: 3, Emoji: "❤️", Change: 2, Version: 2}
	if err := e2e.CheckReactReply(want, 2, summary(2, "❤️")); err != nil {
		t.Fatalf("CheckReactReply(good) = %v", err)
	}
	expectErr(t, e2e.CheckReactReply(want, 1, summary(2, "❤️")), "change 1")
	expectErr(t, e2e.CheckReactReply(want, 2, summary(1, "❤️")), "version 1")
	expectErr(t, e2e.CheckReactReply(want, 2, summary(2, "👍")), "👍=1")
	pin := e2e.Pin{Seq: 4, Version: 1}
	good := []*chatimv1.Pin{{Seq: 4, By: reactor, PinVer: 1, PinnedAt: timestamppb.Now()}}
	if err := e2e.CheckPinReply(pin, reactor, 1, good); err != nil {
		t.Fatalf("CheckPinReply(good) = %v", err)
	}
	expectErr(t, e2e.CheckPinReply(pin, reactor, 2, good), "version 2")
	expectErr(t, e2e.CheckPinReply(pin, "bob", 1, good), "by")
	expectErr(t, e2e.CheckPinReply(pin, reactor, 1, nil), "0 pin")
}

func TestCheckReactionsWantsCountsOnTheReactedSeqOnly(t *testing.T) {
	as := acks(3)
	want := []e2e.Reaction{{Seq: 2, Emoji: "❤️", Change: 2, Version: 2}}
	page := messagesOf(as)
	page[1].Reactions = summary(2, "❤️")
	if err := e2e.CheckReactions(want, page); err != nil {
		t.Fatalf("CheckReactions(reacted) = %v", err)
	}
	if err := e2e.CheckReactions(nil, messagesOf(as)); err != nil {
		t.Fatalf("CheckReactions(none) = %v", err)
	}
	expectErr(t, e2e.CheckReactions(nil, page), "want none")
	expectErr(t, e2e.CheckReactions(want, messagesOf(as)), "seq 2")
	stale := messagesOf(as)
	stale[1].Reactions = summary(1, "❤️")
	expectErr(t, e2e.CheckReactions(want, stale), "version 1")
	expectErr(t, e2e.CheckReactions(want, page[:1]), "missing")
}

func TestCheckMarkEventsWantsTheFinalIDs(t *testing.T) {
	reactions := []e2e.Reaction{{Seq: 3, Emoji: "❤️", Change: 2, Version: 2}}
	pins := []e2e.Pin{{Seq: 4, Version: 1}}
	final := e2e.Event{Kind: e2e.KindReaction, Room: room, ID: e2e.ReactionEventID(room, 3, reactor, 2), Seq: 3, User: reactor, Version: 2, Text: "❤️"}
	earlier := e2e.Event{Kind: e2e.KindReaction, Room: room, ID: e2e.ReactionEventID(room, 3, reactor, 1), Seq: 3, User: reactor, Version: 1, Text: "👍"}
	counts := e2e.Event{Kind: e2e.KindCounts, Room: room, ID: e2e.CountsEventID(room, 3, 2), Seq: 3, Text: "❤️=1"}
	pinned := e2e.Event{Kind: e2e.KindPinned, Room: room, ID: e2e.PinEventID(room, 1), Seq: 4, CID: "a-d"}
	created := e2e.Event{Kind: e2e.KindCreated, Room: room, ID: e2e.MessageEventID(room, 1), Seq: 1}
	missing, err := e2e.CheckMarkEvents(room, reactor, reactions, pins, []e2e.Event{created, earlier, final})
	if err != nil || !slices.Equal(missing, []string{counts.ID, pinned.ID}) {
		t.Fatalf("CheckMarkEvents = %v, %v; want %s and %s missing", missing, err, counts.ID, pinned.ID)
	}
	if missing, err := e2e.CheckMarkEvents(room, reactor, reactions, pins, []e2e.Event{final, counts, pinned, pinned}); err != nil || len(missing) != 0 {
		t.Fatalf("CheckMarkEvents(all, one duplicate) = %v, %v; want none missing", missing, err)
	}
	wrongText := final
	wrongText.Text = "👍"
	bad := map[string]e2e.Event{
		"room 7":              {Kind: e2e.KindCounts, Room: "7", ID: counts.ID, Seq: 3, Text: "❤️=1"},
		"unexpected":          {Kind: e2e.KindReaction, Room: room, ID: e2e.ReactionEventID(room, 5, reactor, 1), Seq: 5, User: reactor, Version: 1},
		`by "bob"`:            {Kind: e2e.KindReaction, Room: room, ID: e2e.ReactionEventID(room, 3, "bob", 1), Seq: 3, User: "bob", Version: 1},
		"want counts_changed": {Kind: e2e.KindReaction, Room: room, ID: counts.ID, Seq: 3, User: reactor, Text: "❤️=1"},
		`"👍", want`:           wrongText,
	}
	for part, ev := range bad {
		_, err := e2e.CheckMarkEvents(room, reactor, reactions, pins, []e2e.Event{ev})
		expectErr(t, err, part)
	}
}
