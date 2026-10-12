package view_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/view"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

var editedAt = time.Unix(1_700_000_000, 0).UTC()

func textMsg(seq uint64, text string) domain.Message {
	return domain.Message{Room: 7, Seq: seq, From: "alice", Text: text, CID: "c-" + text}
}

func TestMaskDeletedDropsOnlyTheTextOfDeletedMessages(t *testing.T) {
	page := []domain.Message{
		{Room: 7, Seq: 1, From: "alice", Text: "kept", Version: 1, EditedAt: editedAt},
		{Room: 7, Seq: 2, From: "bob", Text: "leftover", Version: 3, Deleted: true, EditedAt: editedAt},
	}
	before := slices.Clone(page)
	want := slices.Clone(page)
	want[1].Text = ""
	if got := view.MaskDeleted(view.Viewer{User: "bob"}, page); !reflect.DeepEqual(got, want) {
		t.Fatalf("masked = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(page, before) {
		t.Fatalf("input page was modified")
	}
}

func TestHideForViewerHidesClearedAndHiddenSeqs(t *testing.T) {
	page := []domain.Message{textMsg(1, "a"), textMsg(2, "b"), textMsg(3, "c"), textMsg(4, "d")}
	for i := range page {
		page[i].CreatedAt = editedAt.Add(time.Duration(i) * time.Second)
	}
	before := slices.Clone(page)
	v := view.Viewer{User: "bob", ClearedAt: editedAt, HiddenSeqs: map[uint64]bool{3: true}}
	got := view.HideForViewer(v, page)
	for i, m := range got {
		hidden := m.Seq == 1 || m.Seq == 3
		if m.Hidden != hidden || (m.Text == "") != hidden || m.Seq != page[i].Seq || m.CID != page[i].CID {
			t.Fatalf("message %d = %+v, want hidden=%v with seq and cid kept", i, m, hidden)
		}
	}
	if !reflect.DeepEqual(page, before) {
		t.Fatalf("input page was modified")
	}
	if got := view.HideForViewer(view.Viewer{}, page); !reflect.DeepEqual(got, page) {
		t.Fatalf("zero viewer hid %+v", got)
	}
}

func TestClearedHidesEveryMessageUpToTheMarkTime(t *testing.T) {
	mark := editedAt.Add(time.Minute)
	page := []domain.Message{
		{Room: 7, Thread: 0, Seq: 1, Text: "before", CreatedAt: mark.Add(-time.Second)},
		{Room: 7, Thread: 9, Seq: 2, Text: "same ms in a thread", CreatedAt: mark},
		{Room: 7, Thread: 0, Seq: 3, Text: "after", CreatedAt: mark.Add(time.Millisecond)},
	}
	got := view.HideForViewer(view.Viewer{User: "bob", ClearedAt: mark}, page)
	for i, want := range []bool{true, true, false} {
		if got[i].Hidden != want || (got[i].Text == "") != want {
			t.Fatalf("message %d = %+v, want hidden=%v", i, got[i], want)
		}
	}
	if (view.Viewer{}).Cleared(mark) || !(view.Viewer{ClearedAt: mark}).Cleared(mark) || (view.Viewer{ClearedAt: mark}).Cleared(mark.Add(time.Millisecond)) {
		t.Fatal("Cleared: want a zero mark to clear nothing and a mark to clear up to and including its time")
	}
	if got := view.HideForViewer(view.Viewer{User: "bob"}, page); !reflect.DeepEqual(got, page) {
		t.Fatalf("zero mark hid %+v", got)
	}
}

func TestDefaultPipelineMasksAfterCollapsing(t *testing.T) {
	dup := textMsg(2, "a")
	gone := textMsg(3, "x")
	gone.Deleted = true
	page := []domain.Message{textMsg(1, "a"), dup, gone, textMsg(4, "d")}
	got := view.Default().Apply(view.Viewer{HiddenSeqs: map[uint64]bool{4: true}}, page)
	if want := []uint64{1, 3, 4}; !slices.Equal(seqs(got), want) {
		t.Fatalf("seqs = %v, want %v", seqs(got), want)
	}
	if got[1].Text != "" || !got[1].Deleted || got[2].Text != "" || !got[2].Hidden || got[0].Text != "a" {
		t.Fatalf("page = %+v, want seq 3 masked and seq 4 hidden", got)
	}
}
