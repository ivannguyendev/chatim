package itest

import (
	"strconv"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraGroupMentionWithoutAllNotifiesTheGroupMembersInTheRoom(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastSweep)
	client, mentions := dialCore(t, core.cfg), dialMention(t, core.cfg)
	notices := subscribeNotify(t, it, core.cfg)
	tagGroups(t, mentions, map[string][]string{itUser: {"sales"}, "carol": {"sales"}, "erin": {"sales"}}, "sales")
	roomID := createRoomWith(t, client, []string{itUser, "bob", "carol"})

	group := &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "sales"}}}
	seq := sendLinked(t, client, itUser, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "group-only-1", Text: "@sales", Mentions: group})
	got := awaitNotified(t, notices, "carol")
	n, ok := got["carol"]
	wantID := roomID + "-0-" + strconv.FormatUint(seq, 10) + "-m0-g:sales"
	if !ok || len(got) != 1 || n.GetId() != wantID || n.GetMentionNotified().GetTarget().GetKind() != chatimv1.MentionKind_MENTION_KIND_GROUP {
		t.Fatalf("notifications = %v, want only carol by group with id %s", got, wantID)
	}

	bob := &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "bob"}}}
	sendLinked(t, client, itUser, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "group-only-2", Text: "@bob", Mentions: bob})
	if later := awaitNotified(t, notices, "bob"); len(later) != 1 {
		t.Fatalf("notifications after the group mention = %v, want only bob: the sender and erin (not in the room) get none", later)
	}
}
