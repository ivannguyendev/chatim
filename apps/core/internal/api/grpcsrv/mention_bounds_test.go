package grpcsrv_test

import (
	"fmt"
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func countedRig(t *testing.T, o options) (*mentionRig, *countingLister) {
	counter := &countingLister{}
	o.listing = func(m grpcsrv.MentionLister) grpcsrv.MentionLister {
		counter.MentionLister = m
		return counter
	}
	return newMentionRig(t, o), counter
}

type mentionPage struct {
	texts []string
	calls int
}

func (rg *mentionRig) pages(t *testing.T, counter *countingLister, req *chatimv1.ListMentionsRequest) []mentionPage {
	t.Helper()
	var out []mentionPage
	for range 20 {
		got, _, next := rg.list(t, req)
		out = append(out, mentionPage{texts: got, calls: len(counter.calls())})
		if next == "" {
			return out
		}
		req.Before = next
	}
	t.Fatalf("ListMentions still had a next page after 20 pages: %+v", out)
	return nil
}

func TestListMentionsPushesAnEarliestGroupSinceIntoTheQuery(t *testing.T) {
	rg, counter := countedRig(t, options{})
	a := rg.room(t, 0, "bob")
	for i := 1; i <= 25; i++ {
		rg.post(t, a, fmt.Sprintf("m%02d", i), i, toAll)
	}
	for i := 31; i <= 90; i++ {
		rg.post(t, a, fmt.Sprintf("ops%02d", i), i, toOps)
	}
	groups := []*chatimv1.MentionGroup{{Id: "ops", Since: timestamppb.New(sec(100))}}
	got := rg.pages(t, counter, &chatimv1.ListMentionsRequest{Groups: groups})
	want := []mentionPage{{mentionTexts(25, 6), 1}, {mentionTexts(5, 1), 1}}
	if len(got) != len(want) || !slices.Equal(got[0].texts, want[0].texts) || !slices.Equal(got[1].texts, want[1].texts) || got[0].calls != 1 || got[1].calls != 1 {
		t.Fatalf("pages = %+v, want %+v", got, want)
	}
}

func TestListMentionsBoundsTheScanOfEachPage(t *testing.T) {
	rg, counter := countedRig(t, options{})
	a := rg.room(t, 0, "bob")
	for i := 1; i <= 25; i++ {
		rg.post(t, a, fmt.Sprintf("m%02d", i), i, toAll)
	}
	for i := 31; i <= 110; i++ {
		rg.post(t, a, fmt.Sprintf("ops%03d", i), i, toOps)
	}
	groups := []*chatimv1.MentionGroup{{Id: "ops", Since: timestamppb.New(sec(1000))}, {Id: "hcm"}}
	pages := rg.pages(t, counter, &chatimv1.ListMentionsRequest{Groups: groups})
	var seen []string
	for i, p := range pages {
		if p.calls > 3 {
			t.Fatalf("page %d scanned %d shared rounds, want at most 3", i+1, p.calls)
		}
		seen = append(seen, p.texts...)
	}
	if len(pages[0].texts) != 0 || !slices.Equal(seen, mentionTexts(25, 1)) {
		t.Fatalf("pages = %+v, want an empty capped first page, then every @all once in order", pages)
	}
}

func TestListMentionsJudgesReadByTimeOnThreadsToo(t *testing.T) {
	for name, hide := range map[string][]string{"mark": nil, "hide": {"acme"}} {
		t.Run(name, func(t *testing.T) {
			rg := newMentionRig(t, options{hide: hide})
			a := rg.room(t, 0, "bob")
			for i := 1; i <= 5; i++ {
				rg.post(t, a, fmt.Sprintf("m%d", i), i)
			}
			rg.postIn(t, a, 2, "t-bob", 3, toBob)
			rg.postIn(t, a, 2, "t-all", 3, toAll)
			rg.post(t, a, "late-bob", 6, toBob)
			rg.read(t, a, 4, true)
			wantRead := map[string][]string{"mark": {"late-bob", "t-all", "t-bob"}, "hide": {"late-bob"}}
			got, read, _ := rg.list(t, &chatimv1.ListMentionsRequest{})
			if !slices.Equal(got, wantRead[name]) || !slices.Equal(read, []bool{false, true, true}[:len(got)]) {
				t.Fatalf("after reading seq 4: %v read %v, want %v", got, read, wantRead[name])
			}
			rg.unread(t, a, 2)
			got, read, _ = rg.list(t, &chatimv1.ListMentionsRequest{})
			if !slices.Equal(got, []string{"late-bob", "t-all", "t-bob"}) || slices.Contains(read, true) {
				t.Fatalf("after MarkUnread(2): %v read %v, want all three unread", got, read)
			}
		})
	}
}
