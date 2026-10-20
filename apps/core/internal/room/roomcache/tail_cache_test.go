package roomcache

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestLookupTellsFreshExtendOrReload(t *testing.T) {
	c := newTails(t, 1<<22)
	c.Replace(777, tailOf(1, 44))
	cases := []struct {
		name string
		seq  uint64
		edit bool
		want TailState
	}{
		{"same seq", 44, false, TailFresh},
		{"cache ahead", 43, false, TailFresh},
		{"new messages", 45, false, TailExtend},
		{"new edit", 44, true, TailReload},
		{"new edit and messages", 45, true, TailReload},
	}
	for _, tc := range cases {
		at := editedAt
		if tc.edit {
			at = editedNow
		}
		got, state := c.Lookup(activity(777, tc.seq, at))
		if state != tc.want {
			t.Errorf("%s: state = %d, want %d", tc.name, state, tc.want)
		}
		if state != TailReload && got.LastSeq != 44 {
			t.Errorf("%s: LastSeq = %d, want the cached 44", tc.name, got.LastSeq)
		}
	}
	if _, state := c.Lookup(activity(888, 1, editedAt)); state != TailReload {
		t.Errorf("unknown room: state = %d, want reload", state)
	}
	expectStats(t, "tail", c, 2, 4)
}

func TestMergeAppendsNewerMessagesAndKeepsTheWindowAndPreviews(t *testing.T) {
	c := newTails(t, 1<<22)
	c.Replace(777, tailOf(1, 300))
	newer := tailOf(299, 305)
	merged, ok := c.Merge(777, newer)
	if !ok {
		t.Fatal("Merge refused a same-edit newer tail")
	}
	expectSeqs(t, "window", windowSeqs(merged), 305, 6)
	expectSeqs(t, "previews", previewSeqs(merged), 305, 296)
	if merged.LastSeq != 305 || !merged.LastEditAt.Equal(editedAt) {
		t.Errorf("LastSeq/LastEditAt = %d/%v, want 305/%v", merged.LastSeq, merged.LastEditAt, editedAt)
	}
	if got, state := c.Lookup(activity(777, 305, editedAt)); state != TailFresh || got.LastSeq != 305 {
		t.Errorf("after merge: state %d LastSeq %d, want fresh 305", state, got.LastSeq)
	}
}

func TestMergeRefusesAMissingEntryOrAnotherEdit(t *testing.T) {
	c := newTails(t, 1<<22)
	if _, ok := c.Merge(777, tailOf(1, 3)); ok {
		t.Error("Merge into a missing entry succeeded")
	}
	c.Replace(777, tailOf(1, 3))
	other := tailOf(4, 5)
	other.LastEditAt = editedNow
	if _, ok := c.Merge(777, other); ok {
		t.Error("Merge across a new edit succeeded")
	}
	if got, ok := c.Merge(777, tailOf(1, 2)); !ok || got.LastSeq != 3 {
		t.Errorf("an older tail merged to LastSeq %d (ok %v), want the entry kept at 3", got.LastSeq, ok)
	}
}

func TestReplaceNeverMovesAnEntryBack(t *testing.T) {
	c := newTails(t, 1<<22)
	c.Replace(777, tailOf(1, 10))
	if got := c.Replace(777, tailOf(1, 8)); got.LastSeq != 8 {
		t.Errorf("Replace returned LastSeq %d, want the caller's 8", got.LastSeq)
	}
	if _, state := c.Lookup(activity(777, 10, editedAt)); state != TailFresh {
		t.Error("an older reload replaced a newer entry")
	}
	edited := tailOf(1, 4)
	edited.LastEditAt = editedNow
	c.Replace(777, edited)
	if _, state := c.Lookup(activity(777, 4, editedNow)); state != TailFresh {
		t.Error("a reload after a new edit did not replace the entry")
	}
}

func TestPreviewsAreCutAndCompacted(t *testing.T) {
	c := newTails(t, 1<<22)
	in := tailOf(1, 1)
	in.Previews = []domain.Message{preview(1, longText(domain.PreviewRunes+30))}
	got := c.Replace(777, in)
	p := got.Previews[0]
	if p.Text != longText(domain.PreviewRunes) {
		t.Errorf("preview text has %d bytes, want %d runes", len(p.Text), domain.PreviewRunes)
	}
	if p.Meta != nil || p.Mentions != nil || p.CID != "" || p.From != "lan" || p.Seq != 1 {
		t.Errorf("preview not compacted: %+v", p)
	}
}

func TestTailCacheEvictsByBytes(t *testing.T) {
	one := tailOf(1, 300)
	size := tailBytes(one)
	want := tailEntryBytes + 300*(tailMessageBytes+3) + TailPreviews*(tailPreviewBytes+4+3+4)
	if size != want {
		t.Fatalf("tailBytes = %d, want %d", size, want)
	}
	if 2*size > fullTailBytes || 3*size <= fullTailBytes {
		t.Fatalf("fixture tail of %d bytes does not fit twice in %d", size, fullTailBytes)
	}
	c := newTails(t, fullTailBytes)
	c.Replace(1, one)
	c.Replace(2, one)
	c.Lookup(activity(1, 300, editedAt))
	c.Replace(3, one)
	if c.Bytes() != 2*size || c.Len() != 2 {
		t.Fatalf("Bytes/Len = %d/%d, want %d/2", c.Bytes(), c.Len(), 2*size)
	}
	if _, state := c.Lookup(activity(2, 300, editedAt)); state != TailReload {
		t.Error("room 2 kept, want it evicted as least recently used")
	}
	if _, state := c.Lookup(activity(1, 300, editedAt)); state != TailFresh {
		t.Error("room 1 evicted after its read")
	}
}
