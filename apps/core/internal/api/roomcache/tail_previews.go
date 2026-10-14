package roomcache

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func (t Tail) MissingPreviews() []uint64 {
	have := make(map[uint64]struct{}, len(t.Previews))
	for _, m := range t.Previews {
		have[m.Seq] = struct{}{}
	}
	var out []uint64
	for _, m := range t.Window[:min(len(t.Window), TailPreviews)] {
		if _, ok := have[m.Seq]; !ok {
			out = append(out, m.Seq)
		}
	}
	return out
}

func (c *TailCache) AttachPreviews(room uint64, t Tail, previews []domain.Message) (Tail, bool) {
	own := withPreviews(t, previews)
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.live(room)
	if !ok || e.tail.LastSeq != t.LastSeq || !e.tail.LastEditAt.Equal(t.LastEditAt) {
		return cloneTail(own), false
	}
	filled := withPreviews(e.tail, previews)
	c.entries.put(room, cachedTail{tail: filled, loadedAt: e.loadedAt}, tailBytes(filled))
	return cloneTail(filled), true
}

func withPreviews(t Tail, previews []domain.Message) Tail {
	t.Previews = append(slices.Clone(t.Previews), previews...)
	return settleTail(t)
}
