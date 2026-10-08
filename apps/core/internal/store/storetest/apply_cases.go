package storetest

import (
	"context"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func applyCases() []editCase {
	return []editCase{
		{"apply edit sets text, version and edited time on that message only", applyEdit},
		{"apply delete clears the text and marks the message deleted", applyDelete},
		{"apply at or below the stored version changes nothing", applyStale},
		{"apply on a missing message creates nothing", applyMissing},
		{"the view-only hidden flag is never stored", applyHiddenNeverStored},
		{"cancelled context writes nothing", editsCancelled},
	}
}

func edited(m domain.Message, e domain.Edit) domain.Message {
	m.Version, m.EditedAt, m.Text, m.Deleted = e.Version, e.At, e.Text, e.Kind == domain.EditDelete
	return m
}

func mustApply(t *testing.T, s store.MessageEditor, facts ...domain.Edit) {
	t.Helper()
	for _, e := range facts {
		if err := s.ApplyEdit(t.Context(), e); err != nil {
			t.Fatalf("ApplyEdit(%d/%d/%d v%d): %v", e.Room, e.Thread, e.Seq, e.Version, err)
		}
	}
}

func assertStored(t *testing.T, s store.Messages, want ...domain.Message) {
	t.Helper()
	got, err := s.Find(t.Context(), want[0].Room, keysOf(want))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	assertMessages(t, got, want)
}

func applyEdit(t *testing.T, s editStores) {
	m, other := msg(roomA, mainThread, 1), msg(roomA, mainThread, 2)
	mustInsert(t, s.msgs, []domain.Message{m, other})
	first, second := fact(roomA, mainThread, 1, 1), fact(roomA, mainThread, 1, 2)
	mustApply(t, s.msgs, first)
	assertStored(t, s.msgs, edited(m, first), other)
	mustApply(t, s.msgs, second)
	assertStored(t, s.msgs, edited(m, second), other)
	page, err := s.msgs.Page(t.Context(), store.PageQuery{Room: roomA, Thread: mainThread, Anchor: store.Latest, Limit: 10})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	assertMessages(t, page, []domain.Message{edited(m, second), other})
	if last, err := s.msgs.Last(t.Context(), roomA, mainThread); err != nil || last != 2 {
		t.Fatalf("Last = %d, %v; want 2, edits never move the timeline", last, err)
	}
}

func applyDelete(t *testing.T, s editStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	gone := deletion(roomA, mainThread, 1, 2)
	mustApply(t, s.msgs, fact(roomA, mainThread, 1, 1), gone)
	want := edited(m, gone)
	if want.Text != "" || !want.Deleted || want.Version != 2 {
		t.Fatalf("fixture %+v must be an empty deleted message at version 2", want)
	}
	assertStored(t, s.msgs, want)
}

func applyStale(t *testing.T, s editStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	second := fact(roomA, mainThread, 1, 2)
	mustApply(t, s.msgs, second, fact(roomA, mainThread, 1, 1), deletion(roomA, mainThread, 1, 2))
	assertStored(t, s.msgs, edited(m, second))
}

func applyMissing(t *testing.T, s editStores) {
	e := fact(roomA, mainThread, 1, 1)
	if err := s.msgs.ApplyEdit(t.Context(), e); err != nil {
		t.Fatalf("ApplyEdit(missing message) = %v, want nil", err)
	}
	got, err := s.msgs.Find(t.Context(), roomA, []store.MsgKey{store.EditKeyOf(e)})
	if err != nil || len(got) != 0 {
		t.Fatalf("Find after ApplyEdit on a missing message = %+v, %v; want nothing", got, err)
	}
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	assertStored(t, s.msgs, m)
}

func applyHiddenNeverStored(t *testing.T, s editStores) {
	shown := msg(roomA, mainThread, 1)
	hidden := shown
	hidden.Hidden = true
	mustInsert(t, s.msgs, []domain.Message{hidden})
	assertStored(t, s.msgs, shown)
	e := fact(roomA, mainThread, 1, 1)
	mustApply(t, s.msgs, e)
	assertStored(t, s.msgs, edited(shown, e))
}

func editsCancelled(t *testing.T, s editStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	ctx := cancelledContext(t)
	e := fact(roomA, mainThread, 1, 1)
	assertErrorIs(t, "Append", s.edits.Append(ctx, e), context.Canceled)
	_, _, err := s.edits.Latest(ctx, store.EditKeyOf(e))
	assertErrorIs(t, "Latest", err, context.Canceled)
	assertErrorIs(t, "ApplyEdit", s.msgs.ApplyEdit(ctx, e), context.Canceled)
	_, err = s.hidden.Hide(ctx, "bob", store.KeyOf(m), baseTime)
	assertErrorIs(t, "Hide", err, context.Canceled)
	_, _, err = s.rooms.ClearHistory(ctx, roomA, "alice", baseTime)
	assertErrorIs(t, "ClearHistory", err, context.Canceled)
	if _, ok, err := s.edits.Latest(t.Context(), store.EditKeyOf(e)); ok || err != nil {
		t.Fatalf("Latest after a cancelled Append = %v, %v; want nothing stored", ok, err)
	}
	assertStored(t, s.msgs, m)
	assertMember(t, s.rooms, created(members[0]))
}
