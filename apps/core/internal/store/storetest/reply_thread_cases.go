package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type replyThreadCase struct {
	name string
	run  func(t *testing.T, s store.ReplyThreads)
}

func RunReplyThreads(t *testing.T, open func(t *testing.T) store.ReplyThreads) {
	t.Helper()
	for _, c := range []replyThreadCase{
		{"touch creates the thread and a rerun gives the same result", replyThreadRerun},
		{"an older touch never moves the thread back and a tie keeps the larger seq", replyThreadMonotonic},
		{"list sorts by last reply then parent seq, newest first, one room, paged by cursor", replyThreadList},
		{"touch and list reject bad input", replyThreadInvalid},
	} {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func mustTouchThread(t *testing.T, s store.ReplyThreads, room, parent, reply uint64, at time.Time) {
	t.Helper()
	if err := s.Touch(t.Context(), msgKey(room, mainThread, parent), tenant, reply, at); err != nil {
		t.Fatalf("Touch(%d/%d, reply %d at %v): %v", room, parent, reply, at, err)
	}
}

func listThreads(t *testing.T, s store.ReplyThreads, room uint64, before store.ReplyThreadCursor, limit int) []store.ReplyThread {
	t.Helper()
	got, err := s.List(t.Context(), room, before, limit)
	if err != nil {
		t.Fatalf("List(%d, %+v, %d): %v", room, before, limit, err)
	}
	return got
}

func assertThread(t *testing.T, got store.ReplyThread, parent, reply uint64, at time.Time) {
	t.Helper()
	if got.Parent != msgKey(roomA, mainThread, parent) || got.Tenant != tenant || got.LastReplySeq != reply || !got.LastReplyAt.Equal(at) || got.UpdatedAt.IsZero() {
		t.Fatalf("thread = %+v, want parent %d last reply %d at %v", got, parent, reply, at)
	}
}

func replyThreadRerun(t *testing.T, s store.ReplyThreads) {
	mustTouchThread(t, s, roomA, 40, 57, secs(9))
	first := listThreads(t, s, roomA, store.ReplyThreadCursor{}, 10)
	mustTouchThread(t, s, roomA, 40, 57, secs(9))
	again := listThreads(t, s, roomA, store.ReplyThreadCursor{}, 10)
	if len(first) != 1 || len(again) != 1 || !again[0].UpdatedAt.Equal(first[0].UpdatedAt) {
		t.Fatalf("threads = %+v then %+v, want one unchanged thread", first, again)
	}
	assertThread(t, again[0], 40, 57, secs(9))
}

func replyThreadMonotonic(t *testing.T, s store.ReplyThreads) {
	mustTouchThread(t, s, roomA, 40, 57, secs(9))
	mustTouchThread(t, s, roomA, 40, 60, secs(30))
	mustTouchThread(t, s, roomA, 40, 57, secs(9))
	assertThread(t, listThreads(t, s, roomA, store.ReplyThreadCursor{}, 10)[0], 40, 60, secs(30))
	mustTouchThread(t, s, roomA, 40, 59, secs(30))
	assertThread(t, listThreads(t, s, roomA, store.ReplyThreadCursor{}, 10)[0], 40, 60, secs(30))
	mustTouchThread(t, s, roomA, 40, 61, secs(30))
	assertThread(t, listThreads(t, s, roomA, store.ReplyThreadCursor{}, 10)[0], 40, 61, secs(30))
}

func parentSeqs(threads []store.ReplyThread) []uint64 {
	out := make([]uint64, len(threads))
	for i, th := range threads {
		out[i] = th.Parent.Seq
	}
	return out
}

func replyThreadList(t *testing.T, s store.ReplyThreads) {
	for parent, at := range map[uint64]time.Time{10: secs(3), 20: secs(1), 30: secs(3), 40: secs(2)} {
		mustTouchThread(t, s, roomA, parent, parent+1, at)
	}
	mustTouchThread(t, s, roomB, 5, 6, secs(4))
	pages := []struct {
		before store.ReplyThreadCursor
		want   []uint64
	}{
		{store.ReplyThreadCursor{}, []uint64{30, 10}},
		{store.ReplyThreadCursor{At: secs(3), Seq: 10}, []uint64{40, 20}},
		{store.ReplyThreadCursor{At: secs(1), Seq: 20}, []uint64{}},
		{store.ReplyThreadCursor{At: secs(3), Seq: 30}, []uint64{10, 40}},
	}
	for _, p := range pages {
		if got := parentSeqs(listThreads(t, s, roomA, p.before, 2)); !slices.Equal(got, p.want) {
			t.Fatalf("List(before %+v) parents = %v, want %v", p.before, got, p.want)
		}
	}
	if got := parentSeqs(listThreads(t, s, roomB, store.ReplyThreadCursor{}, 10)); !slices.Equal(got, []uint64{5}) {
		t.Fatalf("List(room B) parents = %v, want [5]", got)
	}
}

func replyThreadInvalid(t *testing.T, s store.ReplyThreads) {
	good := msgKey(roomA, mainThread, 40)
	for name, err := range map[string]error{
		"thread parent": s.Touch(t.Context(), msgKey(roomA, sideThread, 40), tenant, 57, secs(9)),
		"seq 0 parent":  s.Touch(t.Context(), msgKey(roomA, mainThread, 0), tenant, 57, secs(9)),
		"reply seq 0":   s.Touch(t.Context(), good, tenant, 0, secs(9)),
		"zero time":     s.Touch(t.Context(), good, tenant, 57, time.Time{}),
		"no tenant":     s.Touch(t.Context(), good, "", 57, secs(9)),
	} {
		assertErrorIs(t, "Touch("+name+")", err, apperr.ErrInvalidArgument)
	}
	for name, c := range map[string]struct {
		room   uint64
		before store.ReplyThreadCursor
		limit  int
	}{
		"room 0":         {0, store.ReplyThreadCursor{}, 10},
		"limit 0":        {roomA, store.ReplyThreadCursor{}, 0},
		"limit 101":      {roomA, store.ReplyThreadCursor{}, store.MaxPageLimit + 1},
		"seq without at": {roomA, store.ReplyThreadCursor{Seq: 4}, 10},
	} {
		_, err := s.List(t.Context(), c.room, c.before, c.limit)
		assertErrorIs(t, "List("+name+")", err, apperr.ErrInvalidArgument)
	}
	if got := listThreads(t, s, roomA, store.ReplyThreadCursor{}, 10); len(got) != 0 {
		t.Fatalf("threads after rejected touches = %+v, want none", got)
	}
}
