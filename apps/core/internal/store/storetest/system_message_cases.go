package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func systemFieldCases() []messagesCase {
	return []messagesCase{
		{"insert keeps the system kind, skip unread and meta", systemFieldsRoundTrip},
	}
}

func systemEditCases() []editCase {
	return []editCase{
		{"an edit keeps meta and a delete drops it, kind and skip unread stay", systemEditKeepsKind},
	}
}

func systemMessage(seq uint64) domain.Message {
	m := msg(roomA, mainThread, seq)
	m.Kind, m.SkipUnread, m.Meta = domain.KindSystem, true, []byte(`{"event":"member_left","user":"lan"}`)
	return m
}

func systemFieldsRoundTrip(t *testing.T, s store.Messages) {
	plain, sys := msg(roomA, mainThread, 1), systemMessage(2)
	withMeta := msg(roomA, mainThread, 3)
	withMeta.Meta = []byte(`{"call_seconds":180}`)
	mustInsert(t, s, []domain.Message{plain, sys, withMeta})
	assertStored(t, s, plain, sys, withMeta)
	page, err := s.Page(t.Context(), store.PageQuery{Room: roomA, Thread: mainThread, Anchor: store.Latest, Limit: 10})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	assertMessages(t, page, []domain.Message{plain, sys, withMeta})
	if page[0].Meta != nil || page[0].SkipUnread {
		t.Fatalf("plain message = %+v, want no meta and counted as unread", page[0])
	}
}

func systemEditKeepsKind(t *testing.T, s editStores) {
	m := systemMessage(1)
	mustInsert(t, s.msgs, []domain.Message{m})
	text := fact(roomA, mainThread, 1, 1)
	mustApply(t, s.msgs, text)
	assertStored(t, s.msgs, edited(m, text))
	gone := deletion(roomA, mainThread, 1, 2)
	mustApply(t, s.msgs, gone)
	want := edited(m, gone)
	if want.Meta != nil || want.Kind != domain.KindSystem || !want.SkipUnread {
		t.Fatalf("fixture %+v must drop meta and keep kind and skip unread", want)
	}
	assertStored(t, s.msgs, want)
}
