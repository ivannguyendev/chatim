package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func previews(from, to uint64) []domain.Message {
	out := make([]domain.Message, 0, to-from+1)
	for s := to; s >= from; s-- {
		out = append(out, domain.Message{Seq: s, From: "lan", Kind: domain.KindText, Text: "tin " + string(rune('a'+s%26)), CreatedAt: unreadBase.Add(time.Duration(s) * time.Second)})
	}
	return out
}

func TestChoosePreviewTakesTheNewestVisibleMessage(t *testing.T) {
	list := previews(40, 45)
	got, ok := domain.ChoosePreview(list, domain.RoomViewer{User: "minh", Hidden: hidden(45, 44)})
	want := domain.RoomPreview{Seq: 43, From: "lan", Kind: domain.KindText, Text: list[2].Text, CreatedAt: list[2].CreatedAt}
	if !ok || got != want {
		t.Fatalf("ChoosePreview = %+v, %v; want %+v", got, ok, want)
	}
	list[2].Hidden = true
	if got, _ := domain.ChoosePreview(list, domain.RoomViewer{User: "minh", Hidden: hidden(45, 44)}); got.Seq != 42 {
		t.Fatalf("a message hidden by the view = %d, want 42", got.Seq)
	}
}

func TestChoosePreviewKeepsDeletedAndEditedFlags(t *testing.T) {
	list := previews(44, 45)
	list[0].Deleted, list[0].EditedAt, list[0].Text, list[0].Meta = true, unreadBase, "gone", []byte("x")
	got, ok := domain.ChoosePreview(list, domain.RoomViewer{User: "minh"})
	if want := (domain.RoomPreview{Seq: 45, From: "lan", Kind: domain.KindText, CreatedAt: list[0].CreatedAt, Deleted: true}); !ok || got != want {
		t.Fatalf("deleted preview = %+v, %v; want %+v", got, ok, want)
	}
	list[0].Deleted = false
	if got, _ := domain.ChoosePreview(list, domain.RoomViewer{User: "minh"}); !got.Edited || got.Text != "gone" {
		t.Fatalf("edited preview = %+v, want Edited and the text", got)
	}
}

func TestChoosePreviewSkipsClearedHistory(t *testing.T) {
	list := previews(1, 10)
	cleared := domain.RoomViewer{User: "minh", ClearedAt: list[2].CreatedAt}
	if got, ok := domain.ChoosePreview(list, cleared); !ok || got.Seq != 10 {
		t.Fatalf("after clear = %+v, %v; want 10", got, ok)
	}
	cleared.Hidden = hidden(10, 9)
	if got, ok := domain.ChoosePreview(list, cleared); ok || got != (domain.RoomPreview{}) {
		t.Fatalf("nothing visible = %+v, %v; want empty", got, ok)
	}
	if got, ok := domain.ChoosePreview(nil, cleared); ok || got != (domain.RoomPreview{}) {
		t.Fatalf("no previews = %+v, %v; want empty", got, ok)
	}
}

func TestPreviewTextCutsAt120Runes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"xin chào", "xin chào"},
		{strings.Repeat("a", 120), strings.Repeat("a", 120)},
		{strings.Repeat("a", 121), strings.Repeat("a", 120)},
		{strings.Repeat("ạ", 120), strings.Repeat("ạ", 120)},
		{strings.Repeat("ạ", 200), strings.Repeat("ạ", 120)},
		{strings.Repeat("a", 119) + "👍👍", strings.Repeat("a", 119) + "👍"},
	}
	for _, c := range cases {
		if got := domain.PreviewText(c.in); got != c.want {
			t.Errorf("PreviewText(%d bytes) = %q, want %q", len(c.in), got, c.want)
		}
	}
	list := previews(1, 1)
	list[0].Text = strings.Repeat("ạ", 130)
	if got, _ := domain.ChoosePreview(list, domain.RoomViewer{}); got.Text != strings.Repeat("ạ", 120) {
		t.Fatalf("ChoosePreview text has %d bytes, want 120 runes", len(got.Text))
	}
}
