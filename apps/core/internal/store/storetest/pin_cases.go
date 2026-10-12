package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type PinnableRooms interface {
	store.Rooms
	store.PinProjector
}

type pinStores struct {
	rooms PinnableRooms
	pins  store.Pins
}

type pinCase struct {
	name string
	run  func(t *testing.T, s pinStores)
}

func RunPins(t *testing.T, open func(t *testing.T) (PinnableRooms, store.Pins)) {
	t.Helper()
	for _, c := range slices.Concat(pinFactCases(), pinStateCases()) {
		t.Run(c.name, func(t *testing.T) {
			rooms, pins := open(t)
			c.run(t, pinStores{rooms: rooms, pins: pins})
		})
	}
}

func pinFact(room uint64, pv uint32, op domain.PinOp, seq uint64) domain.PinAction {
	return domain.PinAction{
		Room: room, PV: uint64(pv), Tenant: tenant, Op: op, Seq: seq, By: "alice",
		At: baseTime.Add(time.Duration(pv) * time.Second),
	}
}

func pinOf(seq uint64, pv uint32) domain.Pin {
	a := pinFact(roomA, pv, domain.PinOpPin, seq)
	return domain.Pin{Thread: a.Thread, Seq: a.Seq, By: a.By, At: a.At, PV: a.PV}
}

func mustAppendPins(t *testing.T, s store.Pins, facts ...domain.PinAction) {
	t.Helper()
	for _, a := range facts {
		if err := s.Append(t.Context(), a); err != nil {
			t.Fatalf("Append(room %d v%d): %v", a.Room, a.PV, err)
		}
	}
}

func samePinAction(a, b domain.PinAction) bool {
	at, bt := a.At, b.At
	a.At, b.At = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}

func samePin(a, b domain.Pin) bool {
	at, bt := a.At, b.At
	a.At, b.At = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}

func assertPinActions(t *testing.T, op string, got, want []domain.PinAction) {
	t.Helper()
	if !slices.EqualFunc(got, want, samePinAction) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func assertPinAt(t *testing.T, s store.Pins, want domain.PinAction) {
	t.Helper()
	got, err := s.At(t.Context(), want.Room, want.PV)
	if err != nil {
		t.Fatalf("At(room %d v%d): %v", want.Room, want.PV, err)
	}
	assertPinActions(t, "At", []domain.PinAction{got}, []domain.PinAction{want})
}

func assertPinState(t *testing.T, s store.PinProjector, room uint64, want domain.PinState) {
	t.Helper()
	got, err := s.PinState(t.Context(), room)
	if err != nil || got.Version != want.Version || !slices.EqualFunc(got.Pins, want.Pins, samePin) {
		t.Fatalf("PinState(%d) = %+v, %v; want %+v", room, got, err, want)
	}
}

func mustApplyPins(t *testing.T, s store.PinProjector, room, base uint64, st domain.PinState, want bool) {
	t.Helper()
	ok, err := s.ApplyPins(t.Context(), room, base, st)
	if err != nil || ok != want {
		t.Fatalf("ApplyPins(%d, base %d, v%d) = %v, %v; want %v", room, base, st.Version, ok, err, want)
	}
}
