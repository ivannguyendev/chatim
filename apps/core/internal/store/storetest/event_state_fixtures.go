package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type StateSeeder interface {
	SeedState(ctx context.Context, target store.StateTarget, at time.Time) error
}

type EventStateStores struct {
	States      store.EventStates
	Checkpoints store.SweepCheckpoints
	Seeder      StateSeeder
}

func msgState(seq uint64, bit store.StateBit, ver uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateMessages, Bit: bit, Room: roomA, Thread: mainThread, Seq: seq, Ver: ver}
}

func memberState(user string, bit store.StateBit, ver uint64, at time.Time) store.StateTarget {
	return store.StateTarget{Collection: store.StateMembers, Bit: bit, Room: roomA, User: user, Ver: ver, At: at}
}

func roomState(room uint64, bit store.StateBit, ver uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateRooms, Bit: bit, Room: room, Ver: ver}
}

func editState(seq, ver uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateEdits, Bit: store.BitEditFact, Room: roomA, Thread: sideThread, Seq: seq, Ver: ver}
}

func interactionState(kind keys.InteractionKind, user string, ver uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateInteractions, Bit: store.BitInteraction, Room: roomA, Seq: 4, User: user, Kind: kind, Ver: ver}
}

func pinState(pv uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StatePins, Bit: store.BitPinFact, Room: roomA, Seq: pv}
}

func hiddenState(user string, seq uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateHidden, Bit: store.BitHidden, Room: roomA, Thread: sideThread, Seq: seq, User: user}
}

func mustSeedState(t *testing.T, s EventStateStores, at time.Time, targets ...store.StateTarget) {
	t.Helper()
	for _, target := range targets {
		if err := s.Seeder.SeedState(t.Context(), target, at); err != nil {
			t.Fatalf("SeedState(%+v, %v): %v", target, at, err)
		}
	}
}

func mustClearStates(t *testing.T, s EventStateStores, targets ...store.StateTarget) {
	t.Helper()
	if err := s.States.Clear(t.Context(), targets); err != nil {
		t.Fatalf("Clear(%+v): %v", targets, err)
	}
}

func pendingPage(t *testing.T, s EventStateStores, c store.StateCollection, from, before time.Time, after store.PendingCursor, limit int) ([]store.PendingDoc, store.PendingCursor) {
	t.Helper()
	docs, next, err := s.States.Pending(t.Context(), c, from, before, after, limit)
	if err != nil {
		t.Fatalf("Pending(%s, %v, %v, %+v, %d): %v", c, from, before, after, limit, err)
	}
	return docs, next
}

func allPending(t *testing.T, s EventStateStores, c store.StateCollection) []store.PendingDoc {
	t.Helper()
	docs, next := pendingPage(t, s, c, time.Time{}, time.Now().Add(time.Hour), store.PendingCursor{}, store.MaxPendingPage)
	if !next.IsZero() {
		t.Fatalf("Pending(%s) has more than one page", c)
	}
	return docs
}

func pendingSeqs(docs []store.PendingDoc) []uint64 {
	out := make([]uint64, len(docs))
	for i, d := range docs {
		out[i] = d.Seq
	}
	return out
}

func assertPendingDoc(t *testing.T, got, want store.PendingDoc) {
	t.Helper()
	if got.Collection != want.Collection || got.Room != want.Room || got.Thread != want.Thread || got.Seq != want.Seq ||
		got.User != want.User || got.Kind != want.Kind || got.Ver != want.Ver || got.State != want.State || !got.StateAt.Equal(want.StateAt) {
		t.Fatalf("pending doc = %+v, want %+v", got, want)
	}
}

func assertNoPending(t *testing.T, s EventStateStores, c store.StateCollection) {
	t.Helper()
	if docs := allPending(t, s, c); len(docs) != 0 {
		t.Fatalf("Pending(%s) = %+v, want none", c, docs)
	}
}

func assertCheckpoint(t *testing.T, s EventStateStores, c store.StateCollection, want time.Time) {
	t.Helper()
	got, err := s.Checkpoints.Get(t.Context(), c)
	if err != nil || !got.Equal(want) {
		t.Fatalf("Get(%s) = %v, %v, want %v", c, got, err, want)
	}
}

func mustAdvance(t *testing.T, s EventStateStores, c store.StateCollection, at time.Time) {
	t.Helper()
	if err := s.Checkpoints.Advance(t.Context(), c, at); err != nil {
		t.Fatalf("Advance(%s, %v): %v", c, at, err)
	}
}
