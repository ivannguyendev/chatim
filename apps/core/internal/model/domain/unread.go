package domain

import "time"

const (
	UnreadMax    = 100
	UnreadWindow = 300
	PreviewRunes = 120
)

type TailMessage struct {
	Seq        uint64
	From       string
	Deleted    bool
	SkipUnread bool
	CreatedAt  time.Time
}

type RoomViewer struct {
	User      string
	ReadSeq   uint64
	ClearedAt time.Time
	Hidden    map[uint64]struct{}
}

type Unread struct {
	Count  int
	Approx bool
}

type RoomPreview struct {
	Seq       uint64
	From      string
	Kind      Kind
	Text      string
	CreatedAt time.Time
	Deleted   bool
	Edited    bool
}

func CountUnread(window []TailMessage, v RoomViewer) Unread {
	scanned := window[:min(len(window), UnreadWindow)]
	n := 0
	for _, m := range scanned {
		if m.Seq <= v.ReadSeq {
			return Unread{Count: n}
		}
		if v.counts(m) {
			n++
		}
		if n == UnreadMax {
			return Unread{Count: n}
		}
	}
	cut := len(window) >= UnreadWindow && scanned[len(scanned)-1].Seq > v.ReadSeq+1
	return Unread{Count: n, Approx: cut}
}

func ChoosePreview(previews []Message, v RoomViewer) (RoomPreview, bool) {
	for _, m := range previews {
		if m.Hidden || v.hides(m.Seq, m.CreatedAt) {
			continue
		}
		m = m.WithoutDeletedContent()
		return RoomPreview{
			Seq: m.Seq, From: m.From, Kind: m.Kind, Text: PreviewText(m.Text), CreatedAt: m.CreatedAt,
			Deleted: m.Deleted, Edited: !m.Deleted && !m.EditedAt.IsZero(),
		}, true
	}
	return RoomPreview{}, false
}

func PreviewText(text string) string {
	n := 0
	for i := range text {
		if n == PreviewRunes {
			return text[:i]
		}
		n++
	}
	return text
}

func (v RoomViewer) counts(m TailMessage) bool {
	return m.From != v.User && !m.Deleted && !m.SkipUnread && !v.hides(m.Seq, m.CreatedAt)
}

func (v RoomViewer) hides(seq uint64, at time.Time) bool {
	if !v.ClearedAt.IsZero() && !at.After(v.ClearedAt) {
		return true
	}
	_, hidden := v.Hidden[seq]
	return hidden
}
