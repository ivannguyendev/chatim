package store_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func newestFirst(room uint64, secs ...int) []domain.Mention {
	out := make([]domain.Mention, len(secs))
	for i, s := range secs {
		out[i] = hit(room, uint64(s), s, every)
	}
	slices.SortFunc(out, func(a, b domain.Mention) int { return store.CompareMentions(b, a) })
	return out
}

func span(from, to int) []int {
	var out []int
	for s := from; s <= to; s++ {
		out = append(out, s)
	}
	return out
}

func sliceFetch(docs []domain.Mention, limit int, calls *int) store.FetchMentions {
	return func(before store.MentionCursor) ([]domain.Mention, error) {
		*calls++
		var out []domain.Mention
		for _, d := range docs {
			if before.Holds(d) && len(out) < limit {
				out = append(out, d)
			}
		}
		return out, nil
	}
}

func keepWhere(ok func(domain.Mention) bool) store.KeepMentions {
	return func(raw []domain.Mention) ([]domain.Mention, error) {
		return slices.DeleteFunc(slices.Clone(raw), func(m domain.Mention) bool { return !ok(m) }), nil
	}
}

func TestScanMentionsStopsAfterItsRoundsAndReportsWhereItStopped(t *testing.T) {
	docs := newestFirst(1, span(1, 70)...)
	calls := 0
	src, err := store.ScanMentions(5, store.MentionCursor{}, sliceFetch(docs, 5, &calls), keepWhere(func(m domain.Mention) bool { return m.Key.Seq <= 5 }))
	if err != nil || calls != store.MentionRounds || len(src.Items) != 0 || src.End != store.CursorOf(docs[14]) {
		t.Fatalf("capped scan = %+v, %v after %d calls; want no items and end at seq 56 after %d calls", src, err, calls, store.MentionRounds)
	}
	calls = 0
	src, _ = store.ScanMentions(5, store.MentionCursor{}, sliceFetch(docs, 5, &calls), keepWhere(func(m domain.Mention) bool { return m.Key.Seq%2 == 0 }))
	if calls != 2 || !slices.Equal(seqsOf(src.Items), []uint64{70, 68, 66, 64, 62}) || !src.End.At.IsZero() {
		t.Fatalf("filled scan = %v end %+v after %d calls", seqsOf(src.Items), src.End, calls)
	}
	calls = 0
	src, _ = store.ScanMentions(5, store.CursorOf(docs[67]), sliceFetch(docs, 5, &calls), keepWhere(func(domain.Mention) bool { return false }))
	if calls != 1 || len(src.Items) != 0 || !src.End.At.IsZero() {
		t.Fatalf("exhausted scan = %+v after %d calls, want no end", src, calls)
	}
}

func TestPageMentionsCutsAtTheNewestEnd(t *testing.T) {
	user := store.MentionSource{Items: newestFirst(1, 9, 5), End: store.CursorOf(hit(1, 5, 5, every))}
	shared := store.MentionSource{Items: newestFirst(2, 8, 4, 3), End: store.CursorOf(hit(2, 2, 2, every))}
	page := store.PageMentions(5, user, shared)
	if !slices.Equal(seqsOf(page.Items), []uint64{9, 8, 5}) || page.Next != user.End {
		t.Fatalf("page = %v next %+v, want [9 8 5] next at the user end", seqsOf(page.Items), page.Next)
	}
	page = store.PageMentions(3, user, shared)
	if !slices.Equal(seqsOf(page.Items), []uint64{9, 8, 5}) || page.Next != store.CursorOf(page.Items[2]) {
		t.Fatalf("full page = %v next %+v", seqsOf(page.Items), page.Next)
	}
	page = store.PageMentions(5, store.MentionSource{Items: newestFirst(2, 8, 4)})
	if len(page.Items) != 2 || !page.Next.At.IsZero() {
		t.Fatalf("last page = %v next %+v, want no next", seqsOf(page.Items), page.Next)
	}
}

func TestMentionsSettledOnlyWhenNothingNewerCanCome(t *testing.T) {
	full := store.MentionSource{Items: newestFirst(1, 30, 20)}
	capped := store.MentionSource{Items: newestFirst(1, 30), End: store.CursorOf(hit(1, 25, 25, every))}
	cases := map[string]struct {
		src  store.MentionSource
		next int
		want bool
	}{
		"full and newer":    {full, 19, true},
		"full at same time": {full, 20, false},
		"short":             {store.MentionSource{Items: full.Items[:1]}, 0, false},
		"capped, older":     {capped, 24, true},
		"capped, same time": {capped, 25, false},
	}
	for name, c := range cases {
		if got := store.MentionsSettled(2, c.src, at(c.next)); got != c.want {
			t.Errorf("%s: MentionsSettled = %v, want %v", name, got, c.want)
		}
	}
}

func TestPagingCappedSourcesSkipsAndRepeatsNothing(t *testing.T) {
	user := newestFirst(1, span(1, 100)...)
	shared := append(newestFirst(2, span(1, 30)...), hit(1, 50, 50, every))
	slices.SortFunc(shared, func(a, b domain.Mention) int { return store.CompareMentions(b, a) })
	keepUser := keepWhere(func(m domain.Mention) bool { return m.Key.Seq%25 == 0 || m.Key.Seq < 4 })
	var want []domain.Mention
	for _, m := range user {
		if ok, _ := keepUser([]domain.Mention{m}); len(ok) == 1 {
			want = append(want, m)
		}
	}
	want = store.MergeMentions(1000, want, shared)
	var seen []domain.Mention
	before, calls := store.MentionCursor{}, 0
	for range 100 {
		a, _ := store.ScanMentions(5, before, sliceFetch(user, 5, &calls), keepUser)
		b, _ := store.ScanMentions(5, before, sliceFetch(shared, 5, &calls), keepWhere(func(domain.Mention) bool { return true }))
		page := store.PageMentions(5, a, b)
		seen = append(seen, page.Items...)
		if page.Next.At.IsZero() {
			break
		}
		before = page.Next
	}
	same := func(a, b domain.Mention) bool { return a.Key == b.Key }
	if !slices.EqualFunc(seen, want, same) {
		t.Fatalf("paged %v,\nwant %v", seqsOf(seen), seqsOf(want))
	}
}
