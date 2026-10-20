package roomcache

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var (
	editedAt  = time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	editedNow = editedAt.Add(time.Minute)
)

func tailMsg(seq uint64) domain.TailMessage {
	return domain.TailMessage{Seq: seq, From: "lan", CreatedAt: editedAt.Add(time.Duration(seq) * time.Second)}
}

func preview(seq uint64, text string) domain.Message {
	return domain.Message{Room: 777, Seq: seq, Tenant: "acme", From: "lan", Text: text, CID: "c", Meta: []byte("meta"),
		Mentions: []domain.MentionTarget{{}}, CreatedAt: editedAt.Add(time.Duration(seq) * time.Second)}
}

func tailOf(from, to uint64) Tail {
	t := Tail{LastSeq: to, LastEditAt: editedAt}
	for seq := to; seq >= from && seq > 0; seq-- {
		t.Window = append(t.Window, tailMsg(seq))
		if len(t.Previews) < TailPreviews {
			t.Previews = append(t.Previews, preview(seq, "text"))
		}
	}
	return t
}

func activity(room, lastSeq uint64, lastEditAt time.Time) store.RoomActivity {
	return store.RoomActivity{Room: room, LastSeq: lastSeq, LastEditAt: lastEditAt}
}

func newTails(t *testing.T, maxBytes int) *TailCache {
	t.Helper()
	c, err := NewTailCache(maxBytes)
	if err != nil {
		t.Fatalf("NewTailCache: %v", err)
	}
	return c
}

func expectSeqs(t *testing.T, name string, got []uint64, first, last uint64) {
	t.Helper()
	if len(got) != int(first-last+1) || got[0] != first || got[len(got)-1] != last {
		t.Fatalf("%s = %d seqs %v..., want %d..%d descending", name, len(got), got[:min(len(got), 3)], first, last)
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[i-1]-1 {
			t.Fatalf("%s not dense descending at %d: %d after %d", name, i, got[i], got[i-1])
		}
	}
}

func windowSeqs(t Tail) []uint64 {
	out := make([]uint64, 0, len(t.Window))
	for _, m := range t.Window {
		out = append(out, m.Seq)
	}
	return out
}

func previewSeqs(t Tail) []uint64 {
	out := make([]uint64, 0, len(t.Previews))
	for _, m := range t.Previews {
		out = append(out, m.Seq)
	}
	return out
}

func longText(runes int) string { return strings.Repeat("é", runes) }
