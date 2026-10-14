package roomcache

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func windowOnly(from, to uint64) Tail {
	t := tailOf(from, to)
	t.Previews = nil
	return t
}

func previewsOf(from, to uint64) []domain.Message {
	var out []domain.Message
	for seq := to; seq >= from && seq > 0; seq-- {
		out = append(out, preview(seq, "text"))
	}
	return out
}

func TestMissingPreviewsListsTheNewestWindowSeqsWithoutAPreview(t *testing.T) {
	if got := windowOnly(1, 44).MissingPreviews(); !slices.Equal(got, []uint64{44, 43, 42, 41, 40, 39, 38, 37, 36, 35}) {
		t.Errorf("window only: missing = %v, want 44..35", got)
	}
	if got := tailOf(1, 44).MissingPreviews(); len(got) != 0 {
		t.Errorf("full tail: missing = %v, want none", got)
	}
	extended := mergeTails(tailOf(1, 44), windowOnly(45, 46))
	if got := extended.MissingPreviews(); !slices.Equal(got, []uint64{46, 45}) {
		t.Errorf("extended: missing = %v, want [46 45]", got)
	}
	if got := windowOnly(1, 3).MissingPreviews(); !slices.Equal(got, []uint64{3, 2, 1}) {
		t.Errorf("short window: missing = %v, want [3 2 1]", got)
	}
	if got := (Tail{}).MissingPreviews(); len(got) != 0 {
		t.Errorf("empty tail: missing = %v, want none", got)
	}
}

func TestAttachPreviewsFillsTheMatchingEntry(t *testing.T) {
	c := newTails(t, 1<<22)
	loaded := c.Replace(777, windowOnly(1, 44))
	got, ok := c.AttachPreviews(777, loaded, previewsOf(35, 44))
	if !ok {
		t.Fatal("AttachPreviews refused the matching entry")
	}
	expectSeqs(t, "returned previews", previewSeqs(got), 44, 35)
	if got.Previews[0].Meta != nil || got.Previews[0].CID != "" {
		t.Errorf("attached preview not compacted: %+v", got.Previews[0])
	}
	cached, state := c.Lookup(activity(777, 44, editedAt))
	if state != TailFresh {
		t.Fatalf("state %d after attach, want fresh", state)
	}
	expectSeqs(t, "cached previews", previewSeqs(cached), 44, 35)
	if missing := cached.MissingPreviews(); len(missing) != 0 {
		t.Errorf("cached tail still misses %v", missing)
	}
}

func TestAttachPreviewsKeepsTheCachedOlderPreviews(t *testing.T) {
	c := newTails(t, 1<<22)
	c.Replace(777, tailOf(1, 44))
	extended, ok := c.Merge(777, windowOnly(45, 45))
	if !ok {
		t.Fatal("Merge refused")
	}
	got, ok := c.AttachPreviews(777, extended, previewsOf(45, 45))
	if !ok {
		t.Fatal("AttachPreviews refused the matching entry")
	}
	expectSeqs(t, "previews", previewSeqs(got), 45, 36)
	cached, _ := c.Lookup(activity(777, 45, editedAt))
	expectSeqs(t, "cached previews", previewSeqs(cached), 45, 36)
}

func TestAttachPreviewsForAStaleEntryIsIgnored(t *testing.T) {
	c := newTails(t, 1<<22)
	stale := c.Replace(777, windowOnly(1, 44))
	c.Merge(777, windowOnly(45, 45))
	got, ok := c.AttachPreviews(777, stale, previewsOf(35, 44))
	if ok {
		t.Error("AttachPreviews applied to an entry that moved on")
	}
	expectSeqs(t, "caller previews", previewSeqs(got), 44, 35)
	cached, _ := c.Lookup(activity(777, 45, editedAt))
	if len(cached.Previews) != 0 {
		t.Errorf("stale attach changed the cache: previews %v", previewSeqs(cached))
	}
	edited := windowOnly(1, 44)
	edited.LastEditAt = editedNow
	c.Replace(777, edited)
	if _, ok := c.AttachPreviews(777, stale, previewsOf(35, 44)); ok {
		t.Error("AttachPreviews applied across a new edit")
	}
	if _, ok := c.AttachPreviews(888, stale, previewsOf(35, 44)); ok {
		t.Error("AttachPreviews applied to a missing entry")
	}
}

func TestAttachPreviewsKeepsTheEntryTTLAndCopies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTails(t, 1<<22)
		loaded := c.Replace(777, windowOnly(1, 44))
		time.Sleep(TailTTL / 2)
		got, ok := c.AttachPreviews(777, loaded, previewsOf(35, 44))
		if !ok {
			t.Fatal("AttachPreviews refused a live entry")
		}
		got.Previews[0].Text = "mallory"
		if cached, _ := c.Lookup(activity(777, 44, editedAt)); cached.Previews[0].Text != "text" {
			t.Error("mutating the returned tail changed the cache")
		}
		time.Sleep(TailTTL / 2)
		if _, state := c.Lookup(activity(777, 44, editedAt)); state != TailReload {
			t.Errorf("state %d after the TTL of the load, want reload although previews were attached", state)
		}
	})
}
