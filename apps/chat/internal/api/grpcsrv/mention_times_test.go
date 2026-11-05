package grpcsrv_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var clientClaim = timestamppb.New(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))

func claimedTimes(all bool, users ...string) *chatimv1.MentionSet {
	set := mentionSet(all, users...)
	set.AllAt = clientClaim
	for _, t := range set.Targets {
		t.At = clientClaim
	}
	return set
}

func assertStampedByCore(t *testing.T, what string, m *chatimv1.Message) {
	t.Helper()
	if len(m.GetMentionTargets()) == 0 || !m.GetMentionAll() {
		t.Fatalf("%s = %v, want targets and @all", what, m)
	}
	for _, target := range append(m.GetMentionTargets(), &chatimv1.MentionTarget{At: m.GetMentionAllAt()}) {
		if at := target.GetAt().AsTime(); at.Year() == 2000 || at.IsZero() {
			t.Fatalf("%s target %v at %v, want the core's own time", what, target.GetId(), at)
		}
	}
}

func TestClientMentionTimesAreIgnored(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice := as(t, "acme", "alice")
	if _, err := rg.client.SendMessage(alice, &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-1", Text: "v0", Mentions: claimedTimes(true, "bob")}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	assertStampedByCore(t, "sent", historyOf(t, rg, room)[0])
	resp, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, Text: "v1", Mentions: claimedTimes(true, "bob", "carol")})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	assertStampedByCore(t, "edited", resp.GetMessage())
}
