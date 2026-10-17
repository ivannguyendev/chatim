package grpcsrv_test

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errDown = errors.New("store down")

func TestListMentionsDropsAnItemWhoseMessageNoLongerCarriesTheTarget(t *testing.T) {
	rg := newMentionRig(t, options{})
	a := rg.room(t, 0, "bob")
	rg.post(t, a, "user", 1, toBob)
	rg.post(t, a, "group", 2, toOps)
	rg.post(t, a, "all", 3, toAll)
	rg.post(t, a, "kept", 4, toBob, toAll)
	ops := []*chatimv1.MentionGroup{{Id: "ops"}}
	if got, _, _ := rg.list(t, &chatimv1.ListMentionsRequest{Groups: ops}); !slices.Equal(got, []string{"kept", "all", "group", "user"}) {
		t.Fatalf("before the edits = %v", got)
	}
	for seq := uint64(1); seq <= 3; seq++ {
		rg.core.editMessage(domain.MessageKey{Room: a, Seq: seq}, func(m *chatimv1.Message) {
			m.MentionAll = false
			m.MentionTargets = []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "bob"}, {Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "ops"}}
		})
	}
	rg.core.editMessage(domain.MessageKey{Room: a, Seq: 4}, func(m *chatimv1.Message) { m.MentionTargets = nil })
	if got, _, _ := rg.list(t, &chatimv1.ListMentionsRequest{Groups: ops}); !slices.Equal(got, []string{"kept"}) {
		t.Fatalf("after the targets left the messages = %v, want only kept (still @all, newest item first)", got)
	}
}

func TestListMentionsReadsOnlyGroupsTheCatalogMarksTaggable(t *testing.T) {
	rg := newMentionRig(t, options{})
	a := rg.room(t, 0, "bob")
	rg.post(t, a, "ops", 1, toOps)
	rg.post(t, a, "hanoi", 2, toHanoi)
	rg.post(t, a, "ghost", 3, domain.Target{Kind: domain.TargetGroup, ID: "ghost"})
	req := &chatimv1.ListMentionsRequest{Groups: []*chatimv1.MentionGroup{{Id: "ops"}, {Id: "hanoi"}, {Id: "ghost"}}}
	if got, _, _ := rg.list(t, req); !slices.Equal(got, []string{"hanoi", "ops"}) {
		t.Fatalf("mentions = %v, want hanoi and ops, ghost not in the catalog", got)
	}
	if _, err := rg.svc.SetGroups(rg.alice, &chatimv1.SetGroupsRequest{Items: []*chatimv1.GroupItem{{Id: "ops", Name: "Ops", Ver: 2}}}); err != nil {
		t.Fatalf("SetGroups: %v", err)
	}
	if got, _, _ := rg.list(t, req); !slices.Equal(got, []string{"hanoi"}) {
		t.Fatalf("mentions after ops went to state 2 = %v, want only hanoi", got)
	}
}

func TestListMentionsMapsDependencyFailuresToUnavailable(t *testing.T) {
	rg, counter := countedRig(t, options{})
	a := rg.room(t, 0, "bob")
	rg.post(t, a, "all", 1, toAll)
	counter.err = errDown
	_, err := rg.svc.ListMentions(rg.bob, &chatimv1.ListMentionsRequest{})
	expectCode(t, "store down", err, codes.Unavailable)
	counter.err = nil
	rg.core.fail(errDown)
	_, err = rg.svc.ListMentions(rg.bob, &chatimv1.ListMentionsRequest{})
	expectCode(t, "core down", err, codes.Unavailable)
}
