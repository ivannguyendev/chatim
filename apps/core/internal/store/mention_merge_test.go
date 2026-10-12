package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var mergeBase = time.UnixMilli(1_700_000_000_000).UTC()

func at(sec int) time.Time { return mergeBase.Add(time.Duration(sec) * time.Second) }

func hit(room, seq uint64, sec int, target domain.MentionTarget) domain.Mention {
	return domain.Mention{Key: domain.MsgKey{Room: room, Seq: seq}, Target: target, Live: true, CreatedAt: at(sec)}
}

func seqsOf(ms []domain.Mention) []uint64 {
	out := make([]uint64, len(ms))
	for i, m := range ms {
		out[i] = m.Key.Seq
	}
	return out
}

func TestSharedMentionRoomsDropsRoomsWithNothingAfterTheirSinceAndSortsNewestFirst(t *testing.T) {
	rooms := []store.MemberRoom{
		{Room: 1, JoinedAt: at(0)},
		{Room: 2, JoinedAt: at(30), ClearedAt: at(10)},
		{Room: 3, JoinedAt: at(0), ClearedAt: at(50), ReadSeq: 4},
		{Room: 4, JoinedAt: at(0)},
		{Room: 5, JoinedAt: at(5)},
		{Room: 6, JoinedAt: at(0)},
	}
	last := map[uint64]time.Time{1: at(20), 2: at(30), 3: at(60), 4: at(40), 5: at(20), 9: at(99)}
	got := store.SharedMentionRooms(rooms, last)
	want := []store.MentionRoom{
		{Room: 3, Since: at(50), Last: at(60), ReadSeq: 4},
		{Room: 4, Since: at(0), Last: at(40)},
		{Room: 1, Since: at(0), Last: at(20)},
		{Room: 5, Since: at(5), Last: at(20)},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("SharedMentionRooms =\n%+v\nwant\n%+v", got, want)
	}
}

func TestRaiseSinceToReadMovesSinceJustBeforeTheReadMessage(t *testing.T) {
	rooms := []store.MentionRoom{
		{Room: 1, Since: at(0), Last: at(40), ReadSeq: 7},
		{Room: 2, Since: at(0), Last: at(30), ReadSeq: 3},
		{Room: 3, Since: at(50), Last: at(60), ReadSeq: 2},
		{Room: 4, Since: at(0), Last: at(10)},
		{Room: 5, Since: at(0), Last: at(10), ReadSeq: 9},
	}
	if keys := store.ReadMessageKeys(rooms); !slices.Equal(keys, []store.MsgKey{{Room: 1, Seq: 7}, {Room: 2, Seq: 3}, {Room: 3, Seq: 2}, {Room: 5, Seq: 9}}) {
		t.Fatalf("ReadMessageKeys = %v", keys)
	}
	readAt := map[store.MsgKey]time.Time{{Room: 1, Seq: 7}: at(20), {Room: 2, Seq: 3}: at(31), {Room: 3, Seq: 2}: at(10)}
	got := store.RaiseSinceToRead(rooms, readAt)
	want := []store.MentionRoom{
		{Room: 1, Since: at(20).Add(-time.Millisecond), Last: at(40), ReadSeq: 7},
		{Room: 3, Since: at(50), Last: at(60), ReadSeq: 2},
		{Room: 4, Since: at(0), Last: at(10)},
		{Room: 5, Since: at(0), Last: at(10), ReadSeq: 9},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("RaiseSinceToRead =\n%+v\nwant\n%+v", got, want)
	}
}

func TestMergeMentionsSortsNewestFirstAndShowsAMessageOnce(t *testing.T) {
	user := []domain.Mention{hit(1, 5, 50, minh), hit(1, 2, 20, minh)}
	shared := []domain.Mention{hit(1, 5, 50, every), hit(2, 3, 50, ops), hit(1, 1, 10, every), hit(2, 1, 20, every)}
	got := store.MergeMentions(10, user, shared)
	if want := []uint64{3, 5, 1, 2, 1}; !slices.Equal(seqsOf(got), want) {
		t.Fatalf("MergeMentions seqs = %v, want %v", seqsOf(got), want)
	}
	if got[1].Target != minh {
		t.Fatalf("MergeMentions kept %+v for seq 5, want the first list's doc", got[1])
	}
	if got := store.MergeMentions(2, user, shared); !slices.Equal(seqsOf(got), []uint64{3, 5}) {
		t.Fatalf("MergeMentions(limit 2) seqs = %v, want [3 5]", seqsOf(got))
	}
}

func TestMentionsSettledOnlyWhenTheLastItemIsNewerThanTheNextRoom(t *testing.T) {
	full := []domain.Mention{hit(1, 3, 30, every), hit(1, 2, 20, every)}
	cases := map[string]struct {
		merged []domain.Mention
		next   time.Time
		want   bool
	}{
		"newer":     {full, at(19), true},
		"same time": {full, at(20), false},
		"older":     {full, at(25), false},
		"short":     {full[:1], at(0), false},
	}
	for name, c := range cases {
		if got := store.MentionsSettled(c.merged, 2, c.next); got != c.want {
			t.Errorf("%s: MentionsSettled = %v, want %v", name, got, c.want)
		}
	}
}

func TestPageMentionsStopsAtTheEndOfAFullUserPage(t *testing.T) {
	user := []domain.Mention{hit(1, 9, 90, minh), hit(1, 5, 50, minh)}
	shared := []domain.Mention{hit(2, 8, 80, every), hit(2, 4, 40, every), hit(2, 3, 30, every)}
	page := store.PageMentions(3, user, store.MentionCursor{}, shared)
	if !slices.Equal(seqsOf(page.Items), []uint64{9, 8, 5}) || page.Next != store.CursorOf(page.Items[2]) {
		t.Fatalf("full page = %v next %+v", seqsOf(page.Items), page.Next)
	}
	end := store.CursorOf(user[1])
	page = store.PageMentions(5, user, end, shared)
	if !slices.Equal(seqsOf(page.Items), []uint64{9, 8, 5}) || page.Next != end {
		t.Fatalf("page cut at the user end = %v next %+v, want [9 8 5] next %+v", seqsOf(page.Items), page.Next, end)
	}
	page = store.PageMentions(5, user, store.MentionCursor{}, shared)
	if !slices.Equal(seqsOf(page.Items), []uint64{9, 8, 5, 4, 3}) || page.Next != store.CursorOf(page.Items[4]) {
		t.Fatalf("exact page = %v next %+v", seqsOf(page.Items), page.Next)
	}
	page = store.PageMentions(6, user, store.MentionCursor{}, shared)
	if len(page.Items) != 5 || !page.Next.At.IsZero() {
		t.Fatalf("last page = %v next %+v, want 5 items and no next", seqsOf(page.Items), page.Next)
	}
}

func TestSharedMentionQueryWants(t *testing.T) {
	q := store.SharedMentionQuery{Rooms: []store.SharedRoom{{Room: 1, Since: at(10)}, {Room: 2}}, Groups: []string{"ops"}, Before: store.CursorOf(hit(2, 9, 90, every)), Limit: 5}
	other := domain.MentionTarget{Kind: domain.MentionGroup, ID: "hn"}
	retired := hit(2, 4, 40, every)
	retired.Live = false
	cases := map[string]struct {
		m    domain.Mention
		want bool
	}{
		"all after since":   {hit(1, 2, 11, every), true},
		"all at since":      {hit(1, 1, 10, every), false},
		"my group":          {hit(2, 1, 1, ops), true},
		"another group":     {hit(2, 2, 2, other), false},
		"a user doc":        {hit(2, 3, 3, minh), false},
		"another room":      {hit(3, 1, 50, every), false},
		"retired":           {retired, false},
		"at the cursor":     {hit(2, 9, 90, every), false},
		"older tie in time": {hit(2, 8, 90, every), true},
		"newer than cursor": {hit(2, 10, 91, every), false},
	}
	for name, c := range cases {
		if got := q.Wants(c.m); got != c.want {
			t.Errorf("%s: Wants = %v, want %v", name, got, c.want)
		}
	}
}
