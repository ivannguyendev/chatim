package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
)

func fileFieldCases() []messagesCase {
	return []messagesCase{
		{"insert keeps attachments and a message without files has none", filesRoundTrip},
		{"stored attachments are not shared with the caller", filesDetached},
	}
}

func fileEditCases() []editCase {
	return []editCase{
		{"an edit keeps attachments and a delete drops them", filesEditKeepsDeleteDrops},
	}
}

func messageWithFiles(seq uint64) domain.Message {
	m := msg(roomA, mainThread, seq)
	m.Attachments = []domain.Attachment{
		{Key: "acme/alice/20261107/a", Name: "ảnh.png", Mime: "image/png", Size: 2048, Dim: []int32{640, 480}, Dur: 0, Hash: "sha256:ab"},
		{Key: "acme/alice/20261107/b", Name: "clip.mp4", Mime: "video/mp4", Size: 9, Dur: 12000},
		{Key: "acme/alice/20261107/c", Name: "hoa-don.pdf", Mime: "application/pdf", Size: 1},
	}
	return m
}

func filesRoundTrip(t *testing.T, s store.Messages) {
	withFiles, plain := messageWithFiles(1), msg(roomA, mainThread, 2)
	mustInsert(t, s, []domain.Message{withFiles, plain})
	assertStored(t, s, withFiles, plain)
	page, err := s.Page(t.Context(), store.PageQuery{Room: roomA, Thread: mainThread, Anchor: store.Latest, Limit: 10})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	assertMessages(t, page, []domain.Message{withFiles, plain})
	if page[1].Attachments != nil || page[0].Attachments[2].Dim != nil {
		t.Fatalf("page = %+v, want nil attachments and nil dim when absent", page)
	}
}

func filesDetached(t *testing.T, s store.Messages) {
	m := messageWithFiles(1)
	mustInsert(t, s, []domain.Message{m})
	m.Attachments[0].Dim[0] = 1
	got, err := s.Find(t.Context(), roomA, []store.MsgKey{store.KeyOf(m)})
	if err != nil || len(got) != 1 {
		t.Fatalf("Find = %+v, %v", got, err)
	}
	got[0].Attachments[0].Dim[1], got[0].Attachments[1].Name = 2, "changed"
	assertStored(t, s, messageWithFiles(1))
}

func filesEditKeepsDeleteDrops(t *testing.T, s editStores) {
	m := messageWithFiles(1)
	mustInsert(t, s.msgs, []domain.Message{m})
	text := fact(roomA, mainThread, 1, 1)
	mustApply(t, s.msgs, text)
	assertStored(t, s.msgs, edited(m, text))
	gone := deletion(roomA, mainThread, 1, 2)
	mustApply(t, s.msgs, gone)
	want := edited(m, gone)
	if want.Attachments != nil {
		t.Fatalf("fixture %+v must drop attachments", want)
	}
	assertStored(t, s.msgs, want)
}
