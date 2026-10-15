package storetest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type eventStateCase struct {
	name string
	run  func(t *testing.T, s EventStateStores)
}

func RunEventStates(t *testing.T, open func(t *testing.T) EventStateStores) {
	t.Helper()
	for _, c := range []eventStateCase{
		{"pending lists one collection by flag time then key in [from, before), paged by cursor", pendingOrderAndPages},
		{"pending reports the key and the bits of every collection", pendingKeyFields},
		{"a new bit keeps the oldest flag time", pendingKeepsOldestFlagTime},
		{"members flagged at the same time page by key length then bytes, none skipped or repeated", pendingMembersTieByKeyLength},
		{"clear turns off its bit and keeps the others", clearKeepsOtherBits},
		{"clear with another version does nothing", clearStaleVersion},
		{"clear of the last bit drops the doc from pending; missing docs and bits are skipped", clearLastBit},
		{"one clear spans many collections", clearManyCollections},
		{"mark flags an existing doc of the same version now and keeps an older flag time", markExistingDocs},
		{"pending, clear, mark and checkpoints reject bad input and write nothing", eventStateInvalid},
		{"a checkpoint only moves forward, per collection", checkpointMonotonic},
		{"a cancelled context fails every call", eventStateCancelled},
	} {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func pendingOrderAndPages(t *testing.T, s EventStateStores) {
	mustSeedState(t, s, secs(0), msgState(6, store.BitMsgCreated, 0))
	mustSeedState(t, s, secs(1), msgState(3, store.BitMsgCreated, 0))
	mustSeedState(t, s, secs(2), msgState(2, store.BitMsgCreated, 0), msgState(1, store.BitMsgCreated, 0))
	mustSeedState(t, s, secs(3), msgState(5, store.BitReactionCounts, 1))
	mustSeedState(t, s, secs(5), msgState(7, store.BitMsgCreated, 0))
	mustSeedState(t, s, secs(2), roomState(roomA, store.BitRoomCreated, 0), memberState("bob", store.BitMember, 1, time.Time{}))
	var got []uint64
	var pages int
	for after := (store.PendingCursor{}); ; pages++ {
		docs, next := pendingPage(t, s, store.StateMessages, secs(1), secs(5), after, 2)
		got = append(got, pendingSeqs(docs)...)
		if next.IsZero() {
			break
		}
		after = next
	}
	if !slices.Equal(got, []uint64{3, 1, 2, 5}) || pages != 1 {
		t.Fatalf("paged seqs = %v in %d extra pages, want [3 1 2 5] in 1", got, pages)
	}
	docs, next := pendingPage(t, s, store.StateMessages, secs(1), secs(5), store.PendingCursor{}, 4)
	if !slices.Equal(pendingSeqs(docs), []uint64{3, 1, 2, 5}) || !next.IsZero() {
		t.Fatalf("full page = %v next %+v, want [3 1 2 5] and no cursor", pendingSeqs(docs), next)
	}
	assertPendingDoc(t, docs[3], store.PendingDoc{Collection: store.StateMessages, Room: roomA, Seq: 5, State: store.BitReactionCounts, StateAt: secs(3)})
}

func pendingKeyFields(t *testing.T, s EventStateStores) {
	mustSeedState(t, s, secs(1),
		editState(9, 3), interactionState(keys.ReactionKind, "bob", 2), interactionState(keys.BookmarkKind, "bob", 5),
		pinState(4), hiddenState("carol", 8), roomState(roomB, store.BitMemberCount, 2),
		memberState("dave", store.BitReadPosition, 7, time.Time{}), memberState("dave", store.BitPreferences, 2, time.Time{}))
	at := secs(1)
	want := map[store.StateCollection][]store.PendingDoc{
		store.StateEdits: {{Collection: store.StateEdits, Room: roomA, Thread: sideThread, Seq: 9, Ver: 3, State: store.BitEditFact, StateAt: at}},
		store.StateInteractions: {
			{Collection: store.StateInteractions, Room: roomA, Seq: 4, User: "bob", Kind: keys.ReactionKind, State: store.BitInteraction, StateAt: at},
			{Collection: store.StateInteractions, Room: roomA, Seq: 4, User: "bob", Kind: keys.BookmarkKind, State: store.BitInteraction, StateAt: at},
		},
		store.StatePins:    {{Collection: store.StatePins, Room: roomA, Seq: 4, State: store.BitPinFact, StateAt: at}},
		store.StateHidden:  {{Collection: store.StateHidden, Room: roomA, Thread: sideThread, Seq: 8, User: "carol", State: store.BitHidden, StateAt: at}},
		store.StateRooms:   {{Collection: store.StateRooms, Room: roomB, State: store.BitMemberCount, StateAt: at}},
		store.StateMembers: {{Collection: store.StateMembers, Room: roomA, User: "dave", State: store.BitReadPosition | store.BitPreferences, StateAt: at}},
	}
	for c, docs := range want {
		got := allPending(t, s, c)
		if len(got) != len(docs) {
			t.Fatalf("Pending(%s) = %+v, want %+v", c, got, docs)
		}
		for i := range docs {
			assertPendingDoc(t, got[i], docs[i])
		}
	}
	assertNoPending(t, s, store.StateMessages)
}

func pendingKeepsOldestFlagTime(t *testing.T, s EventStateStores) {
	mustSeedState(t, s, secs(5), msgState(1, store.BitMsgCreated, 0))
	mustSeedState(t, s, secs(3), msgState(1, store.BitReactionCounts, 1))
	mustSeedState(t, s, secs(8), msgState(1, store.BitReplyCounts, 1))
	got := allPending(t, s, store.StateMessages)
	if len(got) != 1 {
		t.Fatalf("Pending = %+v, want one doc", got)
	}
	assertPendingDoc(t, got[0], store.PendingDoc{Collection: store.StateMessages, Room: roomA, Seq: 1, State: 7, StateAt: secs(3)})
}

func checkpointMonotonic(t *testing.T, s EventStateStores) {
	assertCheckpoint(t, s, store.StateMessages, time.Time{})
	mustAdvance(t, s, store.StateMessages, secs(5))
	mustAdvance(t, s, store.StateMessages, secs(3))
	assertCheckpoint(t, s, store.StateMessages, secs(5))
	mustAdvance(t, s, store.StateMessages, secs(7))
	assertCheckpoint(t, s, store.StateMessages, secs(7))
	mustAdvance(t, s, store.StateMembers, secs(2))
	assertCheckpoint(t, s, store.StateMembers, secs(2))
	assertCheckpoint(t, s, store.StateMessages, secs(7))
	assertCheckpoint(t, s, store.StateRooms, time.Time{})
}

func eventStateCancelled(t *testing.T, s EventStateStores) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	target := msgState(1, store.BitMsgCreated, 0)
	if _, _, err := s.States.Pending(ctx, store.StateMessages, time.Time{}, secs(1), store.PendingCursor{}, 10); err == nil {
		t.Fatal("Pending with a cancelled context succeeded")
	}
	if err := s.States.Clear(ctx, []store.StateTarget{target}); err == nil {
		t.Fatal("Clear with a cancelled context succeeded")
	}
	if err := s.States.Mark(ctx, store.StateMessages, []store.StateTarget{target}); err == nil {
		t.Fatal("Mark with a cancelled context succeeded")
	}
	if _, err := s.Checkpoints.Get(ctx, store.StateMessages); err == nil {
		t.Fatal("Get with a cancelled context succeeded")
	}
	if err := s.Checkpoints.Advance(ctx, store.StateMessages, secs(1)); err == nil {
		t.Fatal("Advance with a cancelled context succeeded")
	}
}

func eventStateInvalid(t *testing.T, s EventStateStores) {
	good := msgState(1, store.BitMsgCreated, 0)
	mustSeedState(t, s, secs(1), good)
	bad := msgState(0, store.BitMsgCreated, 0)
	assertErrorIs(t, "Clear", s.States.Clear(t.Context(), []store.StateTarget{good, bad}), apperr.ErrInvalidArgument)
	assertErrorIs(t, "Mark(other collection)", s.States.Mark(t.Context(), store.StateRooms, []store.StateTarget{good}), apperr.ErrInvalidArgument)
	assertErrorIs(t, "Mark(bad bit)", s.States.Mark(t.Context(), store.StateMessages, []store.StateTarget{msgState(1, 8, 0)}), apperr.ErrInvalidArgument)
	if got := allPending(t, s, store.StateMessages); len(got) != 1 || got[0].State != store.BitMsgCreated {
		t.Fatalf("Pending after refused calls = %+v, want the seeded doc unchanged", got)
	}
	for name, limit := range map[string]int{"limit 0": 0, "limit over a page": store.MaxPendingPage + 1} {
		_, _, err := s.States.Pending(t.Context(), store.StateMessages, time.Time{}, secs(9), store.PendingCursor{}, limit)
		assertErrorIs(t, "Pending("+name+")", err, apperr.ErrInvalidArgument)
	}
	_, _, err := s.States.Pending(t.Context(), store.StateCollection(0), time.Time{}, secs(9), store.PendingCursor{}, 10)
	assertErrorIs(t, "Pending(unknown collection)", err, apperr.ErrInvalidArgument)
	_, err = s.Checkpoints.Get(t.Context(), store.StateCollection(0))
	assertErrorIs(t, "Get(unknown collection)", err, apperr.ErrInvalidArgument)
	assertErrorIs(t, "Advance(unknown collection)", s.Checkpoints.Advance(t.Context(), store.StateCollection(0), secs(1)), apperr.ErrInvalidArgument)
	assertErrorIs(t, "Advance(zero time)", s.Checkpoints.Advance(t.Context(), store.StateMessages, time.Time{}), apperr.ErrInvalidArgument)
}
