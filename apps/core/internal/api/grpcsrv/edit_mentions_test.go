package grpcsrv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func mentionSet(all bool, users ...string) *chatimv1.MentionSet {
	set := &chatimv1.MentionSet{All: all}
	for _, u := range users {
		set.Targets = append(set.Targets, &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: u})
	}
	return set
}

func TestEditMentionsAreKeptReplacedOrClearedAndListedPerVersion(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice := as(t, "acme", "alice")
	if _, err := rg.client.SendMessage(alice, &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-1", Text: "v0", Mentions: mentionSet(true, "bob")}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	edits := []struct {
		mentions *chatimv1.MentionSet
		want     *chatimv1.MentionSet
	}{
		{nil, mentionSet(true, "bob")},
		{mentionSet(false, "carol", "dan"), mentionSet(false, "carol", "dan")},
		{&chatimv1.MentionSet{}, nil},
	}
	for i, e := range edits {
		resp, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVer: uint32(i), Text: "v", Mentions: e.mentions})
		if err != nil {
			t.Fatalf("EditMessage %d: %v", i+1, err)
		}
		m := resp.GetMessage()
		got := &chatimv1.MentionSet{Targets: m.GetMentionTargets(), All: m.GetMentionAll()}
		if want := e.want; want == nil && (len(got.Targets) > 0 || got.All) || want != nil && !proto.Equal(got, want) {
			t.Fatalf("edit %d mentions = %v, want %v", i+1, got, want)
		}
	}
	history, err := rg.client.GetEditHistory(alice, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1})
	if err != nil {
		t.Fatalf("GetEditHistory: %v", err)
	}
	want := []*chatimv1.MentionSet{mentionSet(true, "bob"), mentionSet(true, "bob"), mentionSet(false, "carol", "dan"), nil}
	vs := history.GetVersions()
	if len(vs) != len(want) {
		t.Fatalf("versions = %v, want %d rows", vs, len(want))
	}
	for i, v := range vs {
		if !proto.Equal(v.GetMentions(), want[i]) {
			t.Fatalf("version %d mentions = %v, want %v", v.GetVer(), v.GetMentions(), want[i])
		}
	}
}
