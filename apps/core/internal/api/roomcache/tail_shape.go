package roomcache

import (
	"cmp"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

const (
	TailWindow   = domain.UnreadWindow
	TailPreviews = 10

	tailEntryBytes   = 160
	tailMessageBytes = 64
	tailPreviewBytes = 320
	maxIDBytes       = 64
	fullTailBytes    = tailEntryBytes + TailWindow*(tailMessageBytes+maxIDBytes) +
		TailPreviews*(tailPreviewBytes+2*maxIDBytes+4*domain.PreviewRunes)
)

type Tail struct {
	LastSeq    uint64
	LastEditAt time.Time
	Window     []domain.TailMessage
	Previews   []domain.Message
}

func settleTail(t Tail) Tail {
	window := slices.Clone(t.Window)
	slices.SortFunc(window, func(a, b domain.TailMessage) int { return cmp.Compare(b.Seq, a.Seq) })
	window = slices.CompactFunc(window, func(a, b domain.TailMessage) bool { return a.Seq == b.Seq })
	previews := make([]domain.Message, 0, len(t.Previews))
	for _, m := range t.Previews {
		previews = append(previews, compactPreview(m))
	}
	slices.SortFunc(previews, func(a, b domain.Message) int { return cmp.Compare(b.Seq, a.Seq) })
	previews = slices.CompactFunc(previews, func(a, b domain.Message) bool { return a.Seq == b.Seq })
	t.Window = slices.Clip(window[:min(len(window), TailWindow)])
	t.Previews = slices.Clip(previews[:min(len(previews), TailPreviews)])
	if len(t.Window) > 0 {
		t.LastSeq = max(t.LastSeq, t.Window[0].Seq)
	}
	return t
}

func mergeTails(base, newer Tail) Tail {
	newer = settleTail(newer)
	if newer.LastSeq <= base.LastSeq {
		return base
	}
	fresh := func(seq uint64) bool { return seq > base.LastSeq }
	window := slices.DeleteFunc(slices.Clone(newer.Window), func(m domain.TailMessage) bool { return !fresh(m.Seq) })
	previews := slices.DeleteFunc(slices.Clone(newer.Previews), func(m domain.Message) bool { return !fresh(m.Seq) })
	return settleTail(Tail{
		LastSeq:    newer.LastSeq,
		LastEditAt: base.LastEditAt,
		Window:     append(window, base.Window...),
		Previews:   append(previews, base.Previews...),
	})
}

func newerTail(a, b Tail) bool {
	if !a.LastEditAt.Equal(b.LastEditAt) {
		return a.LastEditAt.After(b.LastEditAt)
	}
	return a.LastSeq > b.LastSeq
}

func compactPreview(m domain.Message) domain.Message {
	return domain.Message{
		Room: m.Room, Thread: m.Thread, Seq: m.Seq, Tenant: m.Tenant, From: m.From, Kind: m.Kind,
		Text: domain.PreviewText(m.Text), CreatedAt: m.CreatedAt, Version: m.Version, Deleted: m.Deleted,
		EditedAt: m.EditedAt, Hidden: m.Hidden, SkipUnread: m.SkipUnread,
	}
}

func tailBytes(t Tail) int {
	n := tailEntryBytes
	for _, m := range t.Window {
		n += tailMessageBytes + len(m.From)
	}
	for _, m := range t.Previews {
		n += tailPreviewBytes + len(m.Tenant) + len(m.From) + len(m.Text)
	}
	return n
}
