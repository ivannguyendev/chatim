package storetest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type FactStateStores struct {
	Edits        store.Edits
	Interactions store.Interactions
	Pins         store.Pins
	Hidden       store.Hidden
	States       store.EventStates
}

func (s FactStateStores) states() EventStateStores { return EventStateStores{States: s.States} }

type factStateCase struct {
	name string
	run  func(t *testing.T, s FactStateStores)
}

func RunFactStates(t *testing.T, open func(t *testing.T) FactStateStores) {
	t.Helper()
	for _, c := range slices.Concat(factStateCases(), interactionStateCases()) {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func factStateCases() []factStateCase {
	return []factStateCase{
		{"edit facts from ver 1 are flagged at their time and the ver 0 original is not", editFactsFlagged},
		{"an edit fact that already exists flags nothing", editExistingFlagsNothing},
		{"pin facts are flagged at their time and a refused one flags nothing", pinFactsFlagged},
		{"the first hide is flagged at its time and a repeat hide changes nothing", hideFlaggedOnce},
	}
}

func editFactsFlagged(t *testing.T, s FactStateStores) {
	first, second := fact(roomA, sideThread, 1, 1), deletion(roomA, sideThread, 1, 2)
	mustAppend(t, s.Edits, original(roomA, sideThread, 1), first, second)
	got := allPending(t, s.states(), store.StateEdits)
	if len(got) != 2 {
		t.Fatalf("Pending(edits) = %+v, want versions 1 and 2", got)
	}
	for i, e := range []domain.Edit{first, second} {
		want := store.PendingDoc{Collection: store.StateEdits, Room: roomA, Thread: sideThread, Seq: 1, Ver: uint64(e.Version), State: store.BitEditFact, StateAt: e.At}
		assertPendingDoc(t, got[i], want)
	}
}

func editExistingFlagsNothing(t *testing.T, s FactStateStores) {
	first := fact(roomA, sideThread, 1, 1)
	mustAppend(t, s.Edits, first)
	mustClearStates(t, s.states(), editState(1, 1))
	rival := first
	rival.By, rival.At = "bob", secs(9)
	assertErrorIs(t, "Append(existing version)", s.Edits.Append(t.Context(), rival), store.ErrEditExists)
	assertNoPending(t, s.states(), store.StateEdits)
}

func pinFactsFlagged(t *testing.T, s FactStateStores) {
	pin, unpin := pinFact(roomA, 1, domain.PinOpPin, 4), pinFact(roomA, 2, domain.PinOpUnpin, 4)
	mustAppendPins(t, s.Pins, pin, unpin)
	got := allPending(t, s.states(), store.StatePins)
	if len(got) != 2 {
		t.Fatalf("Pending(pins) = %+v, want pin versions 1 and 2", got)
	}
	for i, a := range []domain.PinAction{pin, unpin} {
		assertPendingDoc(t, got[i], store.PendingDoc{Collection: store.StatePins, Room: roomA, Seq: a.PV, State: store.BitPinFact, StateAt: a.At})
	}
	mustClearStates(t, s.states(), pinState(1), pinState(2))
	rival := pin
	rival.Seq, rival.At = 5, secs(9)
	assertErrorIs(t, "Append(existing pin version)", s.Pins.Append(t.Context(), rival), store.ErrPinExists)
	assertNoPending(t, s.states(), store.StatePins)
}

func hideFlaggedOnce(t *testing.T, s FactStateStores) {
	first := hiddenMark("alice", roomA, sideThread, 3, 1)
	mustHide(t, s.Hidden, first, hiddenMark("alice", roomA, sideThread, 3, 5))
	want := store.PendingDoc{Collection: store.StateHidden, Room: roomA, Thread: sideThread, Seq: 3, User: "alice", State: store.BitHidden, StateAt: first.At}
	got := allPending(t, s.states(), store.StateHidden)
	if len(got) != 1 {
		t.Fatalf("Pending(hidden) = %+v, want one doc", got)
	}
	assertPendingDoc(t, got[0], want)
	mustClearStates(t, s.states(), hiddenState("alice", 3))
	mustHide(t, s.Hidden, hiddenMark("alice", roomA, sideThread, 3, 9))
	assertNoPending(t, s.states(), store.StateHidden)
}
