package roomcache

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
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

func TestAttachPreviewsKeepsThePreviewTheEntryAlreadyHolds(t *testing.T) {
	c := newTails(t, 1<<22)
	c.Replace(777, tailOf(1, 44))
	extended, _ := c.Merge(777, windowOnly(45, 45))
	late := []domain.Message{preview(45, "new"), preview(44, "other page")}
	got, ok := c.AttachPreviews(777, extended, late)
	if !ok {
		t.Fatal("AttachPreviews refused the matching entry")
	}
	cached, _ := c.Lookup(activity(777, 45, editedAt))
	for name, tail := range map[string]Tail{"returned": got, "cached": cached} {
		expectSeqs(t, name+" previews", previewSeqs(tail), 45, 36)
		if tail.Previews[0].Text != "new" || tail.Previews[1].Text != "text" {
			t.Errorf("%s previews 45/44 = %q/%q, want new/text", name, tail.Previews[0].Text, tail.Previews[1].Text)
		}
	}
}

func withFiles(m domain.Message) domain.Message {
	m.Attachments = []domain.Attachment{
		{Key: "acme/lan/a", Name: "a.jpg", Mime: "image/jpeg", Size: 10, Dim: []int32{4, 3}, Dur: 0, Hash: "sha256:x"},
		{Key: "acme/lan/b", Name: "b.mp4", Mime: "video/mp4", Size: 20, Dim: []int32{16, 9}, Dur: 5000},
	}
	return m
}

func TestAttachPreviewsKeepsOnlyTheFileMimes(t *testing.T) {
	c := newTails(t, 1<<22)
	loaded := c.Replace(777, windowOnly(1, 44))
	source := previewsOf(35, 44)
	source[0] = withFiles(source[0])
	got, ok := c.AttachPreviews(777, loaded, source)
	if !ok {
		t.Fatal("AttachPreviews refused the matching entry")
	}
	want := []domain.Attachment{{Mime: "image/jpeg"}, {Mime: "video/mp4"}}
	if !reflect.DeepEqual(got.Previews[0].Attachments, want) {
		t.Errorf("returned attachments = %+v, want only mimes", got.Previews[0].Attachments)
	}
	source[0].Attachments[0].Mime = "text/html"
	source[0].Attachments = append(source[0].Attachments[:1], domain.Attachment{Mime: "text/plain"})
	cached, _ := c.Lookup(activity(777, 44, editedAt))
	if !reflect.DeepEqual(cached.Previews[0].Attachments, want) {
		t.Errorf("cached attachments = %+v after the source changed, want %+v", cached.Previews[0].Attachments, want)
	}
	if cached.Previews[1].Attachments != nil {
		t.Errorf("text-only preview attachments = %+v, want nil", cached.Previews[1].Attachments)
	}
}

func TestTailBytesCountsTheFileMimes(t *testing.T) {
	plain := Tail{Previews: []domain.Message{preview(1, "text")}}
	files := Tail{Previews: []domain.Message{compactPreview(withFiles(preview(1, "text")))}}
	if got, want := tailBytes(files)-tailBytes(plain), 2*tailAttachmentBytes+len("image/jpeg")+len("video/mp4"); got != want {
		t.Fatalf("attachment bytes = %d, want %d", got, want)
	}
	id := strings.Repeat("u", maxIDBytes)
	worst := Tail{}
	for seq := uint64(TailWindow); seq > 0; seq-- {
		worst.Window = append(worst.Window, domain.TailMessage{Seq: seq, From: id})
	}
	for seq := uint64(TailPreviews); seq > 0; seq-- {
		m := domain.Message{Seq: seq, Tenant: id, From: id, Text: strings.Repeat("ạ", domain.PreviewRunes)}
		for range domain.MaxAttachments {
			m.Attachments = append(m.Attachments, domain.Attachment{Mime: strings.Repeat("m", domain.MaxAttachmentMimeBytes)})
		}
		worst.Previews = append(worst.Previews, m)
	}
	if got := tailBytes(worst); got > fullTailBytes {
		t.Fatalf("worst tail %d bytes, above fullTailBytes %d", got, fullTailBytes)
	}
}
