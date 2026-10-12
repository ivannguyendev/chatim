package publish_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestReactionAndPinEventsGoToTheirOwnSubjectsWithoutAMark(t *testing.T) {
	m := domain.Message{
		Room: roomA, Seq: 7, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-7", CreatedAt: sentAt,
		Reactions: domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 2},
	}
	r := domain.Reaction{Room: roomA, Seq: 7, Tenant: tenant, User: "bob", Emoji: "👍", N: 1, At: sentAt}
	pin := domain.PinAction{Room: roomA, PV: 3, Tenant: tenant, Op: domain.PinOpPin, Seq: 7, By: "bob", At: sentAt}
	unpin := pin
	unpin.PV, unpin.Op = 4, domain.PinOpUnpin
	cases := map[string]struct {
		ev          *chatimv1.Event
		subject, id string
	}{
		"reaction": {pbconv.ReactionChanged(domain.RoomGroup, r), "evt.acme.message.101.reaction_changed", "101-0-7-bob-n1"},
		"counts":   {pbconv.CountsChanged(domain.RoomGroup, m, sentAt), "evt.acme.message.101.counts_changed", "101-0-7-reactions-v2"},
		"pinned":   {pbconv.PinChanged(domain.RoomGroup, m, pin), "evt.acme.room.101.msg_pinned", "101-p3"},
		"unpinned": {pbconv.PinChanged(domain.RoomGroup, m, unpin), "evt.acme.room.101.msg_unpinned", "101-p4"},
	}
	for name, c := range cases {
		msg, err := publish.Message("evt", roomA, c.ev)
		if err != nil {
			t.Fatalf("%s: Message: %v", name, err)
		}
		if msg.Subject != c.subject || publishtest.MsgID(msg) != c.id {
			t.Fatalf("%s: subject %q msg id %q, want %s and %s", name, msg.Subject, publishtest.MsgID(msg), c.subject, c.id)
		}
		if key, ok := publish.MarkKey(roomA, c.ev); ok {
			t.Fatalf("%s: MarkKey = %v, true; want no mark", name, key)
		}
	}
}
