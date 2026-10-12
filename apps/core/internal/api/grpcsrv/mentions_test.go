package grpcsrv_test

import (
	"fmt"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestListMentionsShowsOnlyWhatTheCallerCouldSeeInTheirRooms(t *testing.T) {
	rg := newMentionRig(t, options{})
	a := rg.room(t, 0, "bob")
	b := rg.room(t, 0)
	c := rg.room(t, 0, "bob")
	d := rg.room(t, 0, "carol")
	rg.post(t, b, "b-all-early", 10, toAll)
	rg.post(t, b, "b-ops-early", 11, toOps)
	rg.post(t, b, "b-bob-early", 12, toBob)
	rg.join(t, b, 20, "bob")
	rg.post(t, b, "b-all", 30, toAll)
	rg.post(t, b, "b-ops", 31, toOps)
	rg.post(t, b, "b-hanoi", 32, toHanoi)
	rg.post(t, c, "c-all-early", 5, toAll)
	rg.post(t, c, "c-bob-early", 6, toBob)
	rg.clear(t, c, 15)
	rg.post(t, c, "c-all", 25, toAll)
	rg.post(t, d, "d-ops", 40, toOps)
	rg.post(t, d, "d-all", 41, toAll)
	rg.post(t, d, "d-bob", 42, toBob)
	rg.post(t, a, "a-every", 50, toBob, toAll, toOps)
	rg.post(t, a, "a-bob", 3, toBob)
	cases := map[string]struct {
		groups []*chatimv1.MentionGroup
		want   []string
	}{
		"with my group":     {[]*chatimv1.MentionGroup{{Id: "ops"}}, []string{"a-every", "b-ops", "b-all", "c-all", "a-bob"}},
		"without groups":    {nil, []string{"a-every", "b-all", "c-all", "a-bob"}},
		"group since":       {[]*chatimv1.MentionGroup{{Id: "ops", Since: timestamppb.New(sec(32))}}, []string{"a-every", "b-all", "c-all", "a-bob"}},
		"group since at it": {[]*chatimv1.MentionGroup{{Id: "ops", Since: timestamppb.New(sec(32))}, {Id: "ops", Since: timestamppb.New(sec(31))}}, []string{"a-every", "b-ops", "b-all", "c-all", "a-bob"}},
	}
	for name, tc := range cases {
		got, _, next := rg.list(t, &chatimv1.ListMentionsRequest{Groups: tc.groups})
		if !slices.Equal(got, tc.want) || next != "" {
			t.Errorf("%s: mentions = %v next %q, want %v and no next page", name, got, next, tc.want)
		}
	}
}

func TestListMentionsReadStateByTenantSetting(t *testing.T) {
	cases := map[string]struct {
		hide   []string
		worker bool
		pages  [][]string
		read   []bool
	}{
		"mark":                   {nil, true, [][]string{mentionTexts(30, 11), mentionTexts(10, 1)}, []bool{false, true}},
		"hide before the worker": {[]string{"acme"}, false, [][]string{mentionTexts(30, 11), nil}, []bool{false, false}},
		"hide after the worker":  {[]string{"acme"}, true, [][]string{mentionTexts(30, 11), nil}, []bool{false, false}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rg := newMentionRig(t, options{hide: tc.hide})
			a := rg.room(t, 0, "bob")
			for i := 1; i <= 30; i++ {
				target := toBob
				if i%2 == 1 {
					target = toAll
				}
				rg.post(t, a, fmt.Sprintf("m%02d", i), i, target)
			}
			rg.read(t, a, 10, tc.worker)
			before := ""
			for p, want := range tc.pages {
				got, read, next := rg.list(t, &chatimv1.ListMentionsRequest{Before: before})
				if !slices.Equal(got, want) || (next == "") != (len(want) < 20) {
					t.Fatalf("page %d = %v next %q, want %v", p+1, got, next, want)
				}
				for i, r := range read {
					if r != tc.read[p] {
						t.Fatalf("page %d item %s read = %v, want %v", p+1, got[i], r, tc.read[p])
					}
				}
				before = next
			}
		})
	}
}

func mentionTexts(from, to int) []string {
	var out []string
	for i := from; i >= to; i-- {
		out = append(out, fmt.Sprintf("m%02d", i))
	}
	return out
}

func TestListMentionsFillsThePageWhenGroupSinceDropsDocs(t *testing.T) {
	rg := newMentionRig(t, options{})
	a := rg.room(t, 0, "bob")
	for i := 1; i <= 25; i++ {
		rg.post(t, a, fmt.Sprintf("m%02d", i), i, toAll)
	}
	for i := 31; i <= 60; i++ {
		rg.post(t, a, fmt.Sprintf("ops%02d", i), i, toOps)
	}
	groups := []*chatimv1.MentionGroup{{Id: "ops", Since: timestamppb.New(sec(100))}}
	got, _, next := rg.list(t, &chatimv1.ListMentionsRequest{Groups: groups})
	if want := mentionTexts(25, 6); !slices.Equal(got, want) || next == "" {
		t.Fatalf("first page = %v next %q, want %v and a next page", got, next, want)
	}
	got, _, next = rg.list(t, &chatimv1.ListMentionsRequest{Groups: groups, Before: next})
	if want := mentionTexts(5, 1); !slices.Equal(got, want) || next != "" {
		t.Fatalf("second page = %v next %q, want %v and no next page", got, next, want)
	}
}

func TestListMentionsStopsEarlyAndPagesAcrossRoomBatches(t *testing.T) {
	counter := &countingLister{}
	rg := newMentionRig(t, options{listing: func(m grpcsrv.MentionLister) grpcsrv.MentionLister {
		counter.MentionLister = m
		return counter
	}})
	const rooms = 250
	for i := 1; i <= rooms; i++ {
		rg.post(t, rg.room(t, 0, "bob"), fmt.Sprintf("r%03d", i), 100+i, toAll)
	}
	var seen []string
	before := ""
	for page := 1; ; page++ {
		got, _, next := rg.list(t, &chatimv1.ListMentionsRequest{Before: before})
		seen = append(seen, got...)
		sizes := []int{}
		for _, c := range counter.calls() {
			sizes = append(sizes, len(c))
		}
		want := []int{200}
		if page > 10 {
			want = []int{200, 50}
		}
		if !slices.Equal(sizes, want) {
			t.Fatalf("page %d queried batches of %v rooms, want %v", page, sizes, want)
		}
		if next == "" {
			break
		}
		before = next
	}
	var want []string
	for i := rooms; i >= 1; i-- {
		want = append(want, fmt.Sprintf("r%03d", i))
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("paged mentions = %v,\nwant %v", seen, want)
	}
}

func TestListMentionsRejectsBadInput(t *testing.T) {
	rg := newMentionRig(t, options{})
	for name, req := range map[string]*chatimv1.ListMentionsRequest{
		"bad group":   {Groups: []*chatimv1.MentionGroup{{Id: "Bad Id"}}},
		"empty group": {Groups: []*chatimv1.MentionGroup{{}}},
		"bad since":   {Groups: []*chatimv1.MentionGroup{{Id: "ops", Since: &timestamppb.Timestamp{Nanos: -1}}}},
		"bad cursor":  {Before: "!!"},
		"over limit":  {Limit: 21},
	} {
		_, err := rg.client.ListMentions(rg.bob, req)
		if err == nil {
			t.Fatalf("%s: ListMentions succeeded, want InvalidArgument", name)
		}
		expectCode(t, err, codes.InvalidArgument)
	}
	if _, _, next := rg.list(t, &chatimv1.ListMentionsRequest{Limit: 20}); next != "" {
		t.Fatalf("limit 20 on nothing gave next %q", next)
	}
}
