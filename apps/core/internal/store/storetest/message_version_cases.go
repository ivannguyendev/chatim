package storetest

import (
	"maps"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func messageVersionCases() []messagesCase {
	return []messagesCase{
		{"message versions of keys across rooms and threads, missing skipped", messageVersionsAcross},
		{"message versions reject bad keys", messageVersionsInvalid},
	}
}

func messageVersionsOf(t *testing.T, s store.Messages) store.MessageVersions {
	t.Helper()
	mv, ok := s.(store.MessageVersions)
	if !ok {
		t.Fatalf("%T does not implement store.MessageVersions", s)
	}
	return mv
}

func messageVersionsAcross(t *testing.T, s store.Messages) {
	a, b, side := msg(roomA, mainThread, 3), msg(roomB, mainThread, 9), msg(roomA, sideThread, 3)
	a.Version, side.Version = 2, 7
	mustInsert(t, s, append(span(roomA, mainThread, 1, 2), a, b, side))
	ks := []store.MsgKey{store.KeyOf(a), store.KeyOf(b), store.KeyOf(side), {Room: roomB, Seq: 40}}
	got, err := messageVersionsOf(t, s).MessageVersions(t.Context(), ks)
	want := map[store.MsgKey]uint32{store.KeyOf(a): 2, store.KeyOf(b): 0, store.KeyOf(side): 7}
	if err != nil || !maps.Equal(got, want) {
		t.Fatalf("MessageVersions = %v, %v; want %v", got, err, want)
	}
	if got, err := messageVersionsOf(t, s).MessageVersions(t.Context(), nil); err != nil || len(got) != 0 {
		t.Fatalf("MessageVersions(nil) = %v, %v; want nothing", got, err)
	}
}

func messageVersionsInvalid(t *testing.T, s store.Messages) {
	many := make([]store.MsgKey, store.MaxMessageLookup+1)
	for i := range many {
		many[i] = store.MsgKey{Room: roomA, Seq: uint64(i + 1)}
	}
	for name, ks := range map[string][]store.MsgKey{"too many": many, "seq 0": {{Room: roomA}}} {
		_, err := messageVersionsOf(t, s).MessageVersions(t.Context(), ks)
		assertErrorIs(t, "MessageVersions("+name+")", err, apperr.ErrInvalidArgument)
	}
}
