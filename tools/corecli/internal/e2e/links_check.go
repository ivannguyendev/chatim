package e2e

import (
	"fmt"
	"slices"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Origin struct {
	Room   string
	Seq    uint64
	Author string
	Text   string
}

func SeqsOf(msgs []*chatimv1.Message) []uint64 {
	out := make([]uint64, len(msgs))
	for i, m := range msgs {
		out[i] = m.GetSeq()
	}
	return out
}

func CheckSeqs(what, room string, got []*chatimv1.Message, want []uint64) error {
	if seqs := SeqsOf(got); !slices.Equal(seqs, want) {
		return fmt.Errorf("%s returned seq %v, want %v", what, seqs, want)
	}
	for _, m := range got {
		if m.GetRoomId() != room {
			return fmt.Errorf("%s returned seq %d of room %s, want room %s", what, m.GetSeq(), m.GetRoomId(), room)
		}
	}
	return nil
}

func CheckReplies(room string, parent uint64, got []*chatimv1.Message, want []uint64) error {
	what := fmt.Sprintf("replies of seq %d", parent)
	if err := CheckSeqs(what, room, got, want); err != nil {
		return err
	}
	for _, m := range got {
		if r := m.GetReplyTo(); r.GetSeq() != parent || r.GetThreadRoot() != 0 {
			return fmt.Errorf("%s: seq %d replies to %v, want seq %d", what, m.GetSeq(), r, parent)
		}
	}
	return nil
}

func CheckReplyCount(m *chatimv1.Message, want int) error {
	if got := m.GetReplyCount().GetCount(); int(got) != want {
		return fmt.Errorf("seq %d counts %d replies (version %d), want %d", m.GetSeq(), got, m.GetReplyCount().GetVer(), want)
	}
	return nil
}

func CheckBookmarks(room string, items []*chatimv1.BookmarkItem, want []uint64) error {
	msgs := make([]*chatimv1.Message, len(items))
	for i, it := range items {
		if !it.GetAvailable() || it.GetMessage().GetText() == "" {
			return fmt.Errorf("bookmark of seq %d is available %v with text %q, want it readable", it.GetMessage().GetSeq(), it.GetAvailable(), it.GetMessage().GetText())
		}
		msgs[i] = it.GetMessage()
	}
	return CheckSeqs("bookmarks", room, msgs, want)
}

func CheckForward(m *chatimv1.Message, src Origin) error {
	f := m.GetForwardFrom()
	switch {
	case f.GetRoomId() != src.Room || f.GetThreadRoot() != 0 || f.GetSeq() != src.Seq || f.GetAuthor() != src.Author:
		return fmt.Errorf("seq %d forwards %v, want room %s seq %d by %s", m.GetSeq(), f, src.Room, src.Seq, src.Author)
	case m.GetText() != src.Text:
		return fmt.Errorf("forward seq %d has text %q, want the source text %q", m.GetSeq(), m.GetText(), src.Text)
	}
	return nil
}
