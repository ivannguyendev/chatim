package storetest

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type EditableMessages interface {
	store.Messages
	store.MessageEditor
}

type ClearableRooms interface {
	store.Rooms
	store.HistoryClearer
}

type editStores struct {
	msgs   EditableMessages
	rooms  ClearableRooms
	edits  store.Edits
	hidden store.Hidden
}

type editCase struct {
	name string
	run  func(t *testing.T, s editStores)
}

func RunEdits(t *testing.T, open func(t *testing.T) (EditableMessages, ClearableRooms, store.Edits, store.Hidden)) {
	t.Helper()
	for _, c := range slices.Concat(factCases(), originalCases(), applyCases(), viewerCases()) {
		t.Run(c.name, func(t *testing.T) {
			msgs, rooms, edits, hidden := open(t)
			c.run(t, editStores{msgs: msgs, rooms: rooms, edits: edits, hidden: hidden})
		})
	}
}

func fact(room, thread, seq uint64, version uint32) domain.Edit {
	e := domain.Edit{
		Room: room, Thread: thread, Seq: seq, Version: version, Kind: domain.EditText, Tenant: tenant, By: "alice",
		Text: fmt.Sprintf("edit %d/%d/%d v%d", room, thread, seq, version),
		At:   baseTime.Add(time.Duration(version) * time.Second),
	}
	return e
}

func original(room, thread, seq uint64) domain.Edit {
	m := msg(room, thread, seq)
	return domain.Edit{
		Room: room, Thread: thread, Seq: seq, Version: 0, Kind: domain.EditOriginal, Tenant: tenant, By: m.From, Text: m.Text, At: m.CreatedAt,
	}
}

func deletion(room, thread, seq uint64, version uint32) domain.Edit {
	e := fact(room, thread, seq, version)
	e.Kind, e.Text = domain.EditDelete, ""
	return e
}

func msgKey(room, thread, seq uint64) store.MsgKey {
	return store.MsgKey{Room: room, Thread: thread, Seq: seq}
}

func mustAppend(t *testing.T, s store.Edits, facts ...domain.Edit) {
	t.Helper()
	for _, e := range facts {
		if err := s.Append(t.Context(), e); err != nil {
			t.Fatalf("Append(%d/%d/%d v%d): %v", e.Room, e.Thread, e.Seq, e.Version, err)
		}
	}
}

func sameEdit(a, b domain.Edit) bool {
	at, bt := a.At, b.At
	a.At, b.At = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}

func assertEdits(t *testing.T, op string, got, want []domain.Edit) {
	t.Helper()
	if !slices.EqualFunc(got, want, sameEdit) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func assertEditAt(t *testing.T, s store.Edits, want domain.Edit) {
	t.Helper()
	got, err := s.At(t.Context(), store.EditKeyOf(want), want.Version)
	if err != nil {
		t.Fatalf("At(%d/%d/%d v%d): %v", want.Room, want.Thread, want.Seq, want.Version, err)
	}
	assertEdits(t, "At", []domain.Edit{got}, []domain.Edit{want})
}
