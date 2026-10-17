package storetest

import (
	"maps"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func sentTimeCases() []messagesCase {
	return []messagesCase{
		{"sent times of keys across rooms and threads, missing skipped", sentTimesAcross},
		{"sent times reject bad keys", sentTimesInvalid},
	}
}

func messageTimesOf(t *testing.T, s store.Messages) store.MessageTimes {
	t.Helper()
	mt, ok := s.(store.MessageTimes)
	if !ok {
		t.Fatalf("%T does not implement store.MessageTimes", s)
	}
	return mt
}

func sentTimesAcross(t *testing.T, s store.Messages) {
	a, b, side := msg(roomA, mainThread, 3), msg(roomB, mainThread, 9), msg(roomA, sideThread, 3)
	a.CreatedAt, b.CreatedAt, side.CreatedAt = baseTime.Add(time.Second), baseTime.Add(2*time.Second), baseTime.Add(3*time.Second)
	mustInsert(t, s, append(span(roomA, mainThread, 1, 2), a, b, side))
	ks := []store.MsgKey{store.KeyOf(a), store.KeyOf(b), store.KeyOf(side), {Room: roomB, Seq: 40}}
	got, err := messageTimesOf(t, s).SentTimes(t.Context(), ks)
	want := map[store.MsgKey]time.Time{store.KeyOf(a): a.CreatedAt, store.KeyOf(b): b.CreatedAt, store.KeyOf(side): side.CreatedAt}
	if err != nil || !maps.EqualFunc(got, want, time.Time.Equal) {
		t.Fatalf("SentTimes = %v, %v; want %v", got, err, want)
	}
	if got, err := messageTimesOf(t, s).SentTimes(t.Context(), nil); err != nil || len(got) != 0 {
		t.Fatalf("SentTimes(nil) = %v, %v; want nothing", got, err)
	}
}

func sentTimesInvalid(t *testing.T, s store.Messages) {
	many := make([]store.MsgKey, store.MaxMessageLookup+1)
	for i := range many {
		many[i] = store.MsgKey{Room: roomA, Seq: uint64(i + 1)}
	}
	for name, ks := range map[string][]store.MsgKey{"too many": many, "seq 0": {{Room: roomA}}} {
		_, err := messageTimesOf(t, s).SentTimes(t.Context(), ks)
		assertErrorIs(t, "SentTimes("+name+")", err, apperr.ErrInvalidArgument)
	}
}
