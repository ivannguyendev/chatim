package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func originalCases() []editCase {
	return []editCase{
		{"the original row is written once and keeps the first content", originalOnce},
		{"latest and history skip the original row and at reads it", originalReads},
		{"purge text up to 0 clears the original row", originalPurgeZero},
		{"purge text up to a version clears the original row and keeps later rows", originalPurgeRange},
		{"apply edit refuses the original row", originalNotProjected},
	}
}

func originalOnce(t *testing.T, s editStores) {
	first := original(roomA, mainThread, 1)
	mustAppend(t, s.edits, first)
	again := first
	again.By, again.Text = "bob", "rival"
	assertErrorIs(t, "Append(original again)", s.edits.Append(t.Context(), again), store.ErrEditExists)
	assertEditAt(t, s.edits, first)
}

func originalReads(t *testing.T, s editStores) {
	key := msgKey(roomA, mainThread, 1)
	mustAppend(t, s.edits, original(roomA, mainThread, 1))
	if _, ok, err := s.edits.Latest(t.Context(), key); ok || err != nil {
		t.Fatalf("Latest(only the original) = %v, %v; want no edit", ok, err)
	}
	got, err := s.edits.History(t.Context(), key, 0, store.MaxEditPage)
	if err != nil || len(got) != 0 {
		t.Fatalf("History(only the original) = %+v, %v; want none", got, err)
	}
	v1, v2 := fact(roomA, mainThread, 1, 1), fact(roomA, mainThread, 1, 2)
	mustAppend(t, s.edits, v2, v1)
	latest, ok, err := s.edits.Latest(t.Context(), key)
	if err != nil || !ok {
		t.Fatalf("Latest = %v, %v; want v2", ok, err)
	}
	assertEdits(t, "Latest", []domain.Edit{latest}, []domain.Edit{v2})
	got, err = s.edits.History(t.Context(), key, 0, store.MaxEditPage)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	assertEdits(t, "History(after 0)", got, []domain.Edit{v1, v2})
	assertEditAt(t, s.edits, original(roomA, mainThread, 1))
}

func originalPurgeZero(t *testing.T, s editStores) {
	row, v1 := original(roomA, mainThread, 1), deletion(roomA, mainThread, 1, 1)
	mustAppend(t, s.edits, row, v1)
	if err := s.edits.PurgeText(t.Context(), store.EditKeyOf(v1), 0); err != nil {
		t.Fatalf("PurgeText(up to 0): %v", err)
	}
	row.Text = ""
	assertEditAt(t, s.edits, row)
	assertEditAt(t, s.edits, v1)
}

func originalPurgeRange(t *testing.T, s editStores) {
	row, v1, v2, v3 := original(roomA, mainThread, 1), fact(roomA, mainThread, 1, 1), fact(roomA, mainThread, 1, 2), fact(roomA, mainThread, 1, 3)
	neighbour := original(roomA, mainThread, 2)
	mustAppend(t, s.edits, row, v1, v2, v3, neighbour)
	if err := s.edits.PurgeText(t.Context(), store.EditKeyOf(v3), 2); err != nil {
		t.Fatalf("PurgeText(up to 2): %v", err)
	}
	row.Text, v1.Text, v2.Text = "", "", ""
	for _, want := range []domain.Edit{row, v1, v2, v3, neighbour} {
		assertEditAt(t, s.edits, want)
	}
}

func originalNotProjected(t *testing.T, s editStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	err := s.msgs.ApplyEdit(t.Context(), original(roomA, mainThread, 1))
	assertErrorIs(t, "ApplyEdit(original)", err, apperr.ErrInvalidArgument)
	assertStored(t, s.msgs, m)
}
