package itest

import (
	"maps"
	"slices"
	"strconv"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraMentionAppIndexesNotifiesAndListsAcrossTwoCores(t *testing.T) {
	it := realInfra(t)
	core1 := startCore(t, it, itFastSweep)
	env := maps.Clone(itFastSweep)
	env["CORE_ID"] = "it-core-2-" + it.suffix
	core2 := startCore(t, it, env)
	client, mentions := dialCore(t, core1.cfg), dialMention(t, core2.cfg)
	notices := subscribeNotify(t, it, core1.cfg)
	tagGroups(t, mentions, map[string][]string{"carol": {"design"}}, "design")
	roomID := createRoomWith(t, client, []string{itUser, "bob", "carol", "dave"})
	room := parseRoom(t, roomID)

	set := &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{
		{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "bob"}, {Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "design"},
	}, All: true}
	seq := sendLinked(t, client, itUser, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "mention-app-1", Text: "hello", Mentions: set})

	docs := awaitStored(t, "mention docs", storedMentions(t, it, core1.cfg, room, seq), func(d []mentionDocView) bool { return len(d) >= 3 })
	targets := make([]string, len(docs))
	for i, d := range docs {
		if d.State != 1 {
			t.Fatalf("mention doc %+v, want live", d)
		}
		targets[i] = d.Target
	}
	slices.Sort(targets)
	if want := []string{"all:" + roomID, "group:design", "user:bob"}; !slices.Equal(targets, want) {
		t.Fatalf("mention targets %v, want %v once each", targets, want)
	}

	got := awaitNotified(t, notices, "bob", "carol", "dave")
	targetKeys := map[string]string{"bob": "u:bob", "carol": "g:design", "dave": "all"}
	for user, kind := range map[string]chatimv1.MentionKind{"bob": chatimv1.MentionKind_MENTION_KIND_USER, "carol": chatimv1.MentionKind_MENTION_KIND_GROUP, "dave": chatimv1.MentionKind_MENTION_KIND_ALL} {
		n := got[user]
		wantID := roomID + "-0-" + strconv.FormatUint(seq, 10) + "-m0-" + targetKeys[user]
		if n.GetId() != wantID || n.GetRoomId() != roomID || n.GetSeq() != seq || n.GetMentionNotified().GetTarget().GetKind() != kind {
			t.Errorf("notification of %s = %v, want id %s by %v", user, n, wantID, kind)
		}
	}
	if _, sender := got[itUser]; sender {
		t.Errorf("the sender %s was notified", itUser)
	}

	for user, req := range map[string]*chatimv1.ListMentionsRequest{
		"bob": {}, "carol": {Groups: []*chatimv1.MentionGroup{{Id: "design"}}}, "dave": {},
	} {
		resp := awaitStored(t, "mentions of "+user, func() (*chatimv1.ListMentionsResponse, error) {
			return mentions.ListMentions(callerAs(t.Context(), user), req)
		}, func(r *chatimv1.ListMentionsResponse) bool { return len(r.GetItems()) > 0 })
		if seqs := mentionSeqs(resp); !slices.Equal(seqs, []uint64{seq}) || resp.GetItems()[0].GetRead() {
			t.Errorf("ListMentions of %s = %v, want seq %d unread", user, resp, seq)
		}
	}
}
