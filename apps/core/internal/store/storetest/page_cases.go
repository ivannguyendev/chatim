package storetest

import (
	"context"
	"fmt"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func pageCases() []messagesCase {
	return []messagesCase{
		{"oldest page is seq 1..N", pageOldest},
		{"anchors and boundaries", pageAnchors},
		{"empty timeline", pageEmpty},
		{"invalid limit or anchor", pageInvalid},
		{"thread and room isolation", pageIsolation},
		{"gaps in seq", pageGaps},
		{"cancelled context", pageCancelled},
	}
}

type pageWant struct {
	name   string
	anchor store.Anchor
	seq    uint64
	limit  int
	want   []uint64
}

func page(t *testing.T, s store.Messages, room, thread uint64, anchor store.Anchor, seq uint64, limit int) []domain.Message {
	t.Helper()
	q := store.PageQuery{Room: room, Thread: thread, Anchor: anchor, Seq: seq, Limit: limit}
	got, err := s.Page(t.Context(), q)
	if err != nil {
		t.Fatalf("Page(%+v): %v", q, err)
	}
	return got
}

func runPageTable(t *testing.T, s store.Messages, room, thread uint64, tests []pageWant) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := page(t, s, room, thread, tt.anchor, tt.seq, tt.limit)
			assertSeqs(t, got, room, thread, tt.want)
		})
	}
}

func pageOldest(t *testing.T, s store.Messages) {
	mustInsert(t, s, span(roomA, mainThread, 1, 30))
	assertMessages(t, page(t, s, roomA, mainThread, store.Oldest, 0, 10), span(roomA, mainThread, 1, 10))
}

func pageAnchors(t *testing.T, s store.Messages) {
	mustInsert(t, s, span(roomA, mainThread, 1, 30))
	runPageTable(t, s, roomA, mainThread, []pageWant{
		{"latest 10", store.Latest, 0, 10, seqRange(21, 30)},
		{"latest 1", store.Latest, 0, 1, seqRange(30, 30)},
		{"latest ignores seq", store.Latest, 17, 5, seqRange(26, 30)},
		{"latest above size", store.Latest, 0, store.MaxPageLimit, seqRange(1, 30)},
		{"oldest 1", store.Oldest, 0, 1, seqRange(1, 1)},
		{"oldest ignores seq", store.Oldest, 17, 5, seqRange(1, 5)},
		{"oldest above size", store.Oldest, 0, store.MaxPageLimit, seqRange(1, 30)},
		{"before middle", store.Before, 11, 5, seqRange(6, 10)},
		{"before near start", store.Before, 3, 5, seqRange(1, 2)},
		{"before first", store.Before, 1, 5, nil},
		{"before zero", store.Before, 0, 5, nil},
		{"before end", store.Before, 31, 3, seqRange(28, 30)},
		{"before far past end", store.Before, 1000, store.MaxPageLimit, seqRange(1, 30)},
		{"after zero", store.After, 0, 3, seqRange(1, 3)},
		{"after middle", store.After, 10, 5, seqRange(11, 15)},
		{"after near end", store.After, 25, 10, seqRange(26, 30)},
		{"after second last", store.After, 29, 5, seqRange(30, 30)},
		{"after last", store.After, 30, 5, nil},
		{"after far past end", store.After, 1000, 5, nil},
	})
}

func pageEmpty(t *testing.T, s store.Messages) {
	mustInsert(t, s, span(roomA, sideThread, 1, 5))
	mustInsert(t, s, span(roomB, mainThread, 1, 5))
	runPageTable(t, s, roomA, mainThread, []pageWant{
		{"latest", store.Latest, 0, 10, nil},
		{"oldest", store.Oldest, 0, 10, nil},
		{"before", store.Before, 10, 10, nil},
		{"after", store.After, 0, 10, nil},
	})
}

func pageInvalid(t *testing.T, s store.Messages) {
	mustInsert(t, s, span(roomA, mainThread, 1, 5))
	tests := []struct {
		anchor store.Anchor
		limit  int
	}{
		{store.Latest, 0},
		{store.Latest, -1},
		{store.Oldest, store.MaxPageLimit + 1},
		{store.After, 1 << 20},
		{0, 10},
		{store.After + 1, 10},
		{255, 10},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("anchor %d limit %d", tt.anchor, tt.limit), func(t *testing.T) {
			q := store.PageQuery{Room: roomA, Thread: mainThread, Anchor: tt.anchor, Limit: tt.limit}
			got, err := s.Page(t.Context(), q)
			assertErrorIs(t, "Page", err, apperr.ErrInvalidArgument)
			if len(got) != 0 {
				t.Fatalf("invalid Page returned %+v, want nothing", got)
			}
		})
	}
}

func pageIsolation(t *testing.T, s store.Messages) {
	timelines := []struct {
		name         string
		room, thread uint64
		size         uint64
	}{
		{"room a main", roomA, mainThread, 5},
		{"room a side thread", roomA, sideThread, 3},
		{"room b main", roomB, mainThread, 4},
	}
	for _, tl := range timelines {
		mustInsert(t, s, span(tl.room, tl.thread, 1, tl.size))
	}
	for _, tl := range timelines {
		t.Run(tl.name, func(t *testing.T) {
			want := span(tl.room, tl.thread, 1, tl.size)
			assertMessages(t, page(t, s, tl.room, tl.thread, store.Latest, 0, store.MaxPageLimit), want)
			assertMessages(t, page(t, s, tl.room, tl.thread, store.Oldest, 0, store.MaxPageLimit), want)
			assertMessages(t, page(t, s, tl.room, tl.thread, store.Before, 1000, store.MaxPageLimit), want)
			assertMessages(t, page(t, s, tl.room, tl.thread, store.After, 0, store.MaxPageLimit), want)
		})
	}
}

func pageGaps(t *testing.T, s store.Messages) {
	mustInsert(t, s, []domain.Message{
		msg(roomA, mainThread, 10),
		msg(roomA, mainThread, 4),
		msg(roomA, mainThread, 8),
		msg(roomA, mainThread, 2),
		msg(roomA, mainThread, 6),
	})
	runPageTable(t, s, roomA, mainThread, []pageWant{
		{"latest 2", store.Latest, 0, 2, []uint64{8, 10}},
		{"oldest 2", store.Oldest, 0, 2, []uint64{2, 4}},
		{"before a gap", store.Before, 7, 10, []uint64{2, 4, 6}},
		{"before a present seq", store.Before, 6, 1, []uint64{4}},
		{"after a gap", store.After, 3, 2, []uint64{4, 6}},
		{"after a present seq", store.After, 4, 2, []uint64{6, 8}},
		{"after the highest", store.After, 10, 5, nil},
		{"before the lowest", store.Before, 2, 5, nil},
	})
}

func pageCancelled(t *testing.T, s store.Messages) {
	mustInsert(t, s, span(roomA, mainThread, 1, 3))
	q := store.PageQuery{Room: roomA, Thread: mainThread, Anchor: store.Latest, Limit: 10}
	_, err := s.Page(cancelledContext(t), q)
	assertErrorIs(t, "Page", err, context.Canceled)
}
