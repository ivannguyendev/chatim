package storetest

import (
	"context"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func lastCases() []messagesCase {
	return []messagesCase{
		{"empty timeline is zero", lastEmpty},
		{"highest seq per timeline", lastHighest},
		{"cancelled context", lastCancelled},
	}
}

func findCases() []messagesCase {
	return []messagesCase{
		{"existing keys ascending, missing skipped, repeats once", findExisting},
		{"no keys returns nothing", findNoKeys},
		{"key from another room is rejected", findOtherRoom},
		{"cancelled context", findCancelled},
	}
}

func assertLast(t *testing.T, s store.Messages, room, thread, wantSeq uint64) {
	t.Helper()
	seq, err := s.Last(t.Context(), room, thread)
	if err != nil || seq != wantSeq {
		t.Fatalf("Last(%d, %d) = %d, %v; want %d, nil", room, thread, seq, err, wantSeq)
	}
}

func lastEmpty(t *testing.T, s store.Messages) {
	assertLast(t, s, roomA, mainThread, 0)
	mustInsert(t, s, span(roomA, sideThread, 1, 3))
	mustInsert(t, s, span(roomB, mainThread, 1, 3))
	assertLast(t, s, roomA, mainThread, 0)
}

func lastHighest(t *testing.T, s store.Messages) {
	mustInsert(t, s, []domain.Message{
		msg(roomA, mainThread, 3),
		msg(roomA, mainThread, 1),
		msg(roomA, mainThread, 5),
		msg(roomA, mainThread, 2),
		msg(roomA, sideThread, 9),
		msg(roomB, mainThread, 20),
	})
	assertLast(t, s, roomA, mainThread, 5)
	assertLast(t, s, roomA, sideThread, 9)
	assertLast(t, s, roomB, mainThread, 20)
}

func lastCancelled(t *testing.T, s store.Messages) {
	mustInsert(t, s, span(roomA, mainThread, 1, 3))
	_, err := s.Last(cancelledContext(t), roomA, mainThread)
	assertErrorIs(t, "Last", err, context.Canceled)
}

func findExisting(t *testing.T, s store.Messages) {
	mustInsert(t, s, span(roomA, mainThread, 1, 10))
	mustInsert(t, s, span(roomA, sideThread, 1, 3))
	mustInsert(t, s, span(roomB, mainThread, 1, 10))
	keys := []store.MsgKey{
		{Room: roomA, Thread: sideThread, Seq: 2},
		{Room: roomA, Thread: mainThread, Seq: 9},
		{Room: roomA, Thread: mainThread, Seq: 2},
		{Room: roomA, Thread: mainThread, Seq: 42},
		{Room: roomA, Thread: mainThread, Seq: 5},
		{Room: roomA, Thread: mainThread, Seq: 2},
		{Room: roomA, Thread: sideThread, Seq: 9},
	}
	got, err := s.Find(t.Context(), roomA, keys)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	want := []domain.Message{
		msg(roomA, mainThread, 2),
		msg(roomA, mainThread, 5),
		msg(roomA, mainThread, 9),
		msg(roomA, sideThread, 2),
	}
	assertMessages(t, got, want)
}

func findNoKeys(t *testing.T, s store.Messages) {
	mustInsert(t, s, span(roomA, mainThread, 1, 3))
	got, err := s.Find(t.Context(), roomA, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("Find(no keys) = %+v, %v; want nothing", got, err)
	}
}

func findOtherRoom(t *testing.T, s store.Messages) {
	mustInsert(t, s, span(roomA, mainThread, 1, 3))
	mustInsert(t, s, span(roomB, mainThread, 1, 3))
	keys := []store.MsgKey{{Room: roomA, Seq: 1}, {Room: roomB, Seq: 1}}
	got, err := s.Find(t.Context(), roomA, keys)
	assertErrorIs(t, "Find", err, apperr.ErrInvalidArgument)
	if len(got) != 0 {
		t.Fatalf("Find with a foreign key returned %+v, want nothing", got)
	}
}

func findCancelled(t *testing.T, s store.Messages) {
	mustInsert(t, s, span(roomA, mainThread, 1, 3))
	_, err := s.Find(cancelledContext(t), roomA, []store.MsgKey{{Room: roomA, Seq: 1}})
	assertErrorIs(t, "Find", err, context.Canceled)
}
