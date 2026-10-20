package grpcsrv_test

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func at(target domain.Target, second int) domain.Target {
	target.At = sec(second)
	return target
}

func (rg *mentionRig) allPages(t *testing.T, req *chatimv1.ListMentionsRequest) [][]string {
	t.Helper()
	var out [][]string
	for range 20 {
		got, _, next := rg.list(t, req)
		out = append(out, got)
		if next == "" {
			return out
		}
		req.Before = next
	}
	t.Fatalf("ListMentions still had a next page after 20 pages: %v", out)
	return nil
}

func TestListMentionsListsAMessageOnceAtItsNewestReasonAcrossPages(t *testing.T) {
	rg := newMentionRig(t, options{})
	a := rg.room(t, 0, "bob")
	rg.post(t, a, "M", 3, at(toOps, 3), at(toAll, 5))
	rg.post(t, a, "X", 4, toBob)
	pages := rg.allPages(t, &chatimv1.ListMentionsRequest{Limit: 2, Groups: []*chatimv1.MentionGroup{{Id: "ops"}}})
	if got := slices.Concat(pages...); !slices.Equal(got, []string{"M", "X"}) {
		t.Fatalf("pages = %v, want M once at its @all time before X", pages)
	}
}

func TestListMentionsKeepsAnOlderReasonWhenTheNewerOneIsNotTheViewers(t *testing.T) {
	rg := newMentionRig(t, options{})
	rg.catalog(t, domain.Group{ID: "ghost", Name: "Ghost", Active: false, Ver: 1})
	a := rg.room(t, 0, "bob", "carol")
	rg.post(t, a, "other user", 10, at(toBob, 10), at(domain.Target{Kind: domain.TargetUser, ID: "carol"}, 50))
	rg.post(t, a, "other group", 9, at(toBob, 9), at(toHanoi, 51))
	rg.post(t, a, "untaggable", 8, at(toBob, 8), at(domain.Target{Kind: domain.TargetGroup, ID: "ghost"}, 52))
	rg.post(t, a, "before since", 7, at(toBob, 7), at(toOps, 53))
	for i := range 4 {
		rg.post(t, a, "filler", 20+i, toBob)
	}
	groups := []*chatimv1.MentionGroup{{Id: "ops", Since: timestamppb.New(sec(60))}, {Id: "ghost"}}
	pages := rg.allPages(t, &chatimv1.ListMentionsRequest{Limit: 2, Groups: groups})
	want := []string{"filler", "filler", "filler", "filler", "other user", "other group", "untaggable", "before since"}
	if got := slices.Concat(pages...); !slices.Equal(got, want) {
		t.Fatalf("pages = %v, want %v", pages, want)
	}
}
