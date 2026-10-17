package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

func TestSharedMentionRoomsDropsRoomsWithNothingAfterTheirSinceAndSortsNewestFirst(t *testing.T) {
	rooms := []domain.MemberRoom{
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

func TestRaiseSinceToReadMovesSinceToTheReadMessage(t *testing.T) {
	rooms := []store.MentionRoom{
		{Room: 1, Since: at(0), Last: at(40), ReadSeq: 7},
		{Room: 2, Since: at(0), Last: at(30), ReadSeq: 3},
		{Room: 3, Since: at(50), Last: at(60), ReadSeq: 2},
		{Room: 4, Since: at(0), Last: at(10)},
	}
	readAt := map[uint64]time.Time{1: at(20), 2: at(30), 3: at(10)}
	got := store.RaiseSinceToRead(rooms, readAt)
	want := []store.MentionRoom{
		{Room: 1, Since: at(20), Last: at(40), ReadSeq: 7},
		{Room: 3, Since: at(50), Last: at(60), ReadSeq: 2},
		{Room: 4, Since: at(0), Last: at(10)},
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

func TestSharedMentionQueryWants(t *testing.T) {
	q := store.SharedMentionQuery{Tenant: "acme", Rooms: []store.SharedRoom{{Room: 1, Since: at(10)}, {Room: 2}}, Groups: []string{"ops"}, GroupSince: at(1), Before: store.CursorOf(hit(2, 9, 90, every)), Limit: 5}
	other := domain.Target{Kind: domain.TargetGroup, ID: "hn"}
	foreign := hit(2, 6, 60, every)
	foreign.Tenant = "globex"
	retired := hit(2, 4, 40, every)
	retired.State = domain.StateRetired
	cases := map[string]struct {
		m    domain.Mention
		want bool
	}{
		"all after since":    {hit(1, 2, 11, every), true},
		"all at since":       {hit(1, 1, 10, every), false},
		"my group":           {hit(2, 1, 1, ops), true},
		"before group since": {hit(2, 5, 0, ops), false},
		"another tenant":     {foreign, false},
		"another group":      {hit(2, 2, 2, other), false},
		"a user doc":         {hit(2, 3, 3, minh), false},
		"another room":       {hit(3, 1, 50, every), false},
		"retired":            {retired, false},
		"at the cursor":      {hit(2, 9, 90, every), false},
		"older tie in time":  {hit(2, 8, 90, every), true},
		"newer than cursor":  {hit(2, 10, 91, every), false},
	}
	for name, c := range cases {
		if got := q.Wants(c.m); got != c.want {
			t.Errorf("%s: Wants = %v, want %v", name, got, c.want)
		}
	}
}
