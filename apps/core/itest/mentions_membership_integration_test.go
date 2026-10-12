package itest

import (
	"slices"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraSharedMentionsNeedMembershipAtSendTime(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoomWith(t, client, []string{itUser})
	shared := &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "design"}}, All: true}
	sendLinked(t, client, itUser, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "outside-1", Text: "before bob", Mentions: shared})
	addMembersAs(t, client, itUser, roomID, "outside-add", "bob")
	after := sendLinked(t, client, itUser, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "outside-2", Text: "after bob", Mentions: &chatimv1.MentionSet{All: true}})
	req := &chatimv1.ListMentionsRequest{Groups: []*chatimv1.MentionGroup{{Id: "design"}}}
	got := awaitStored(t, "mentions of bob", func() (*chatimv1.ListMentionsResponse, error) {
		return client.ListMentions(callerAs(t.Context(), "bob"), req)
	}, func(resp *chatimv1.ListMentionsResponse) bool { return len(resp.GetItems()) > 0 })
	if seqs := mentionSeqs(got); !slices.Equal(seqs, []uint64{after}) || got.GetItems()[0].GetRead() || got.GetNext() != "" {
		t.Fatalf("ListMentions = %v, want only seq %d unread and no next page", got, after)
	}
}
