package grpcsrv_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type mentionRig struct {
	*rig
	alice, bob context.Context
	a, b, c    string
	base       time.Time
}

func newMentionRig(t *testing.T) *mentionRig {
	t.Helper()
	rg := &mentionRig{rig: newRig(t, options{}), alice: as(t, "acme", "alice"), bob: as(t, "acme", "bob"), base: time.Now().UTC().Truncate(time.Millisecond)}
	rg.a = rg.createGroup(t, "acme", "alice", "bob")
	rg.b = rg.createGroup(t, "acme", "alice", "bob", "carol")
	rg.c = rg.createGroup(t, "acme", "alice", "bob")
	bob := domain.MentionTarget{Kind: domain.MentionUser, ID: "bob"}
	ops := domain.MentionTarget{Kind: domain.MentionGroup, ID: "ops"}
	all := domain.MentionTarget{Kind: domain.MentionAll}
	rg.mention(t, rg.a, "a1", 1, bob)
	rg.mention(t, rg.a, "a2", 2, bob)
	rg.mention(t, rg.b, "b1", 3, ops)
	rg.mention(t, rg.b, "b2", 4, all)
	rg.mention(t, rg.c, "c1", 5, all)
	rg.mention(t, rg.c, "c2", 6, bob)
	rg.mention(t, rg.a, "a3", 7, bob)
	rg.mention(t, rg.b, "b3", 8, domain.MentionTarget{Kind: domain.MentionUser, ID: "carol"})
	rg.mention(t, rg.a, "a4", 0, ops)
	if _, err := rg.client.DeleteMessage(rg.alice, &chatimv1.DeleteMessageRequest{RoomId: rg.a, Seq: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if _, err := rg.client.HideMessage(rg.bob, &chatimv1.HideMessageRequest{RoomId: rg.a, Seq: 3}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	if _, err := rg.client.LeaveRoom(rg.bob, &chatimv1.LeaveRoomRequest{RoomId: rg.c}); err != nil {
		t.Fatalf("LeaveRoom: %v", err)
	}
	return rg
}

func (rg *mentionRig) mention(t *testing.T, room, text string, sec int, target domain.MentionTarget) {
	t.Helper()
	ack := rg.send(t, rg.alice, room, "c-"+text, text)
	id, err := ids.ParseRoomID(room)
	if err != nil {
		t.Fatalf("ParseRoomID: %v", err)
	}
	at := rg.base.Add(time.Duration(sec) * time.Second)
	set := store.MentionSet{Key: store.MsgKey{Room: id, Seq: ack.GetSeq()}, Tenant: "acme", Sender: "alice", Targets: []domain.MentionTarget{target}, CreatedAt: at, At: at}
	if err := rg.mentions.ApplyMentions(t.Context(), set); err != nil {
		t.Fatalf("ApplyMentions(%s): %v", text, err)
	}
}

func (rg *mentionRig) list(t *testing.T, req *chatimv1.ListMentionsRequest) ([]string, string) {
	t.Helper()
	resp, err := rg.client.ListMentions(rg.bob, req)
	if err != nil {
		t.Fatalf("ListMentions(%v): %v", req, err)
	}
	var texts []string
	for _, m := range resp.GetMessages() {
		texts = append(texts, m.GetText())
	}
	return texts, resp.GetNext()
}

func TestListMentionsGivesReadableMessagesNewestFirst(t *testing.T) {
	rg := newMentionRig(t)
	since := timestamppb.New(rg.base.Add(time.Second))
	got, next := rg.list(t, &chatimv1.ListMentionsRequest{Groups: []*chatimv1.MentionGroup{{Id: "ops", Since: since}, {Id: "ops", Since: timestamppb.New(rg.base.Add(time.Hour))}}})
	if want := []string{"b2", "b1", "a1"}; !slices.Equal(got, want) || next != "" {
		t.Fatalf("mentions = %v next %q, want %v and no next page", got, next, want)
	}
	got, _ = rg.list(t, &chatimv1.ListMentionsRequest{Groups: []*chatimv1.MentionGroup{{Id: "ops", Since: since}, {Id: "ops"}}})
	if want := []string{"b2", "b1", "a1", "a4"}; !slices.Equal(got, want) {
		t.Fatalf("mentions without since = %v, want %v", got, want)
	}
	got, _ = rg.list(t, &chatimv1.ListMentionsRequest{})
	if want := []string{"b2", "a1"}; !slices.Equal(got, want) {
		t.Fatalf("mentions without groups = %v, want %v", got, want)
	}
}

func TestListMentionsPagesByMentionDocs(t *testing.T) {
	rg := newMentionRig(t)
	groups := []*chatimv1.MentionGroup{{Id: "ops"}}
	var pages [][]string
	before := ""
	for range 5 {
		got, next := rg.list(t, &chatimv1.ListMentionsRequest{Groups: groups, Before: before, Limit: 2})
		pages = append(pages, got)
		if next == "" {
			break
		}
		before = next
	}
	want := [][]string{nil, {"b2", "b1"}, {"a1"}, {"a4"}}
	if len(pages) != len(want) {
		t.Fatalf("pages = %v, want %v", pages, want)
	}
	for i := range want {
		if !slices.Equal(pages[i], want[i]) {
			t.Fatalf("pages = %v, want %v", pages, want)
		}
	}
}

func TestListMentionsRejectsBadInput(t *testing.T) {
	rg := newMentionRig(t)
	for name, req := range map[string]*chatimv1.ListMentionsRequest{
		"bad group":   {Groups: []*chatimv1.MentionGroup{{Id: "Bad Id"}}},
		"empty group": {Groups: []*chatimv1.MentionGroup{{}}},
		"bad since":   {Groups: []*chatimv1.MentionGroup{{Id: "ops", Since: &timestamppb.Timestamp{Nanos: -1}}}},
		"bad cursor":  {Before: "!!"},
		"over limit":  {Limit: 101},
	} {
		_, err := rg.client.ListMentions(rg.bob, req)
		if err == nil {
			t.Fatalf("%s: ListMentions succeeded, want InvalidArgument", name)
		}
		expectCode(t, err, codes.InvalidArgument)
	}
}
