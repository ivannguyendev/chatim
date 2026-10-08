package storetest

import (
	"context"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func insertCases() []messagesCase {
	return []messagesCase{
		{"one result per message in input order", insertMixedBatch},
		{"duplicate leaves the stored message unchanged", insertDuplicateKeepsOriginal},
		{"same key twice in one call inserts the first", insertSameKeyTwice},
		{"zero room or seq is rejected and not stored", insertRejectsZeroKey},
		{"empty batch returns no results", insertEmpty},
		{"concurrent inserts store each key once", insertConcurrent},
		{"cancelled context stores nothing", insertCancelled},
	}
}

func insertMixedBatch(t *testing.T, s store.Messages) {
	mustInsert(t, s, []domain.Message{msg(roomA, mainThread, 1)})
	batch := []domain.Message{
		msg(roomA, mainThread, 2),
		msg(roomA, mainThread, 0),
		msg(roomA, mainThread, 1),
		msg(roomA, sideThread, 1),
		msg(0, mainThread, 3),
		msg(roomB, mainThread, 1),
	}
	want := []store.Outcome{store.Inserted, store.Rejected, store.Duplicate, store.Inserted, store.Rejected, store.Inserted}
	assertOutcomes(t, s.Insert(t.Context(), batch), want)
	got, err := s.Page(t.Context(), store.PageQuery{Room: roomA, Thread: mainThread, Anchor: store.Oldest, Limit: 10})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	assertMessages(t, got, span(roomA, mainThread, 1, 2))
}

func insertDuplicateKeepsOriginal(t *testing.T, s store.Messages) {
	orig := msg(roomA, mainThread, 1)
	mustInsert(t, s, []domain.Message{orig})
	changed := orig
	changed.Text, changed.CID = "changed", "cid-other"
	assertOutcomes(t, s.Insert(t.Context(), []domain.Message{changed}), []store.Outcome{store.Duplicate})
	got, err := s.Find(t.Context(), roomA, []store.MsgKey{store.KeyOf(orig)})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	assertMessages(t, got, []domain.Message{orig})
	assertLast(t, s, roomA, mainThread, orig.Seq)
}

func insertSameKeyTwice(t *testing.T, s store.Messages) {
	first := msg(roomA, mainThread, 5)
	second := first
	second.Text, second.CID = "second", "cid-second"
	assertOutcomes(t, s.Insert(t.Context(), []domain.Message{first, second}), []store.Outcome{store.Inserted, store.Duplicate})
	got, err := s.Find(t.Context(), roomA, []store.MsgKey{store.KeyOf(first)})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	assertMessages(t, got, []domain.Message{first})
}

func insertRejectsZeroKey(t *testing.T, s store.Messages) {
	batch := []domain.Message{msg(roomA, mainThread, 0), msg(0, mainThread, 1), msg(0, sideThread, 0)}
	assertOutcomes(t, s.Insert(t.Context(), batch), []store.Outcome{store.Rejected, store.Rejected, store.Rejected})
	assertLast(t, s, roomA, mainThread, 0)
	assertLast(t, s, 0, mainThread, 0)
	got, err := s.Find(t.Context(), 0, keysOf(batch[1:]))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	assertMessages(t, got, nil)
}

func insertEmpty(t *testing.T, s store.Messages) {
	if got := s.Insert(t.Context(), nil); len(got) != 0 {
		t.Fatalf("Insert(nil) = %v, want no results", got)
	}
}

func insertConcurrent(t *testing.T, s store.Messages) {
	const writers, perWriter = 8, 50
	batch := span(roomA, mainThread, 1, perWriter)
	results := make([][]store.Result, writers)
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() { results[w] = s.Insert(t.Context(), batch) })
	}
	wg.Wait()
	inserted := make([]int, perWriter)
	for w, res := range results {
		if len(res) != perWriter {
			t.Fatalf("writer %d got %d results, want %d", w, len(res), perWriter)
		}
		for i, r := range res {
			switch r.Outcome {
			case store.Inserted:
				inserted[i]++
			case store.Duplicate:
			default:
				t.Fatalf("writer %d seq %d = %v (%v), want inserted or duplicate", w, batch[i].Seq, r.Outcome, r.Err)
			}
		}
	}
	for i, n := range inserted {
		if n != 1 {
			t.Errorf("seq %d inserted %d times, want exactly once", batch[i].Seq, n)
		}
	}
	got, err := s.Page(t.Context(), store.PageQuery{Room: roomA, Thread: mainThread, Anchor: store.Oldest, Limit: perWriter})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	assertMessages(t, got, batch)
}

func insertCancelled(t *testing.T, s store.Messages) {
	batch := span(roomA, mainThread, 1, 3)
	res := s.Insert(cancelledContext(t), batch)
	if len(res) != len(batch) {
		t.Fatalf("Insert returned %d results, want %d", len(res), len(batch))
	}
	for i, r := range res {
		if r.Outcome != store.Rejected && r.Outcome != store.Unknown {
			t.Errorf("result[%d] = %v, want rejected or unknown", i, r.Outcome)
		}
		assertErrorIs(t, "Insert", r.Err, context.Canceled)
	}
	assertLast(t, s, roomA, mainThread, 0)
}
