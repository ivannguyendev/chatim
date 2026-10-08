package storetest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	walkSize     uint64 = 1000
	walkStride   uint64 = 7919
	walkBatch           = 100
	maxWalkPages        = 100
)

func walkCases() []messagesCase {
	return []messagesCase{
		{"after walk from the start yields every seq once", walkForward},
		{"before walk from the latest yields every seq once", walkBackward},
	}
}

func insertWalkTimeline(t *testing.T, s store.Messages) {
	t.Helper()
	all := make([]domain.Message, 0, walkSize)
	for i := range walkSize {
		all = append(all, msg(roomA, mainThread, (i*walkStride)%walkSize+1))
	}
	for chunk := range slices.Chunk(all, walkBatch) {
		mustInsert(t, s, chunk)
	}
	mustInsert(t, s, span(roomA, sideThread, 1, 5))
	mustInsert(t, s, span(roomB, mainThread, 1, 5))
}

func expectWalkStep(t *testing.T, m domain.Message, want uint64) {
	t.Helper()
	if m.Room != roomA || m.Thread != mainThread || m.Seq != want {
		t.Fatalf("walk got %+v, want seq %d of %d/%d", store.KeyOf(m), want, roomA, mainThread)
	}
}

func expectPageSize(t *testing.T, got []domain.Message, limit int) {
	t.Helper()
	if len(got) > limit {
		t.Fatalf("page has %d messages, limit %d", len(got), limit)
	}
}

func walkForward(t *testing.T, s store.Messages) {
	insertWalkTimeline(t, s)
	const limit = 37
	next := uint64(1)
	for range maxWalkPages {
		got := page(t, s, roomA, mainThread, store.After, next-1, limit)
		if len(got) == 0 {
			break
		}
		expectPageSize(t, got, limit)
		for _, m := range got {
			expectWalkStep(t, m, next)
			next++
		}
	}
	if next != walkSize+1 {
		t.Fatalf("after walk ended at seq %d, want %d", next-1, walkSize)
	}
}

func walkBackward(t *testing.T, s store.Messages) {
	insertWalkTimeline(t, s)
	const limit = 41
	want := walkSize
	got := page(t, s, roomA, mainThread, store.Latest, 0, limit)
	for range maxWalkPages {
		if len(got) == 0 {
			break
		}
		expectPageSize(t, got, limit)
		for _, m := range slices.Backward(got) {
			expectWalkStep(t, m, want)
			want--
		}
		got = page(t, s, roomA, mainThread, store.Before, got[0].Seq, limit)
	}
	if want != 0 {
		t.Fatalf("before walk stopped above seq %d, want every seq down to 1", want)
	}
}
