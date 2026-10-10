package pinproj_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestCurrentFoldsFactsAfterTheStoredVersionWithoutWriting(t *testing.T) {
	rooms, pins := newRooms(t), memstore.NewPins()
	p := projector(t, pins, rooms)
	if got, err := p.Current(t.Context(), room); err != nil || !same(got, domain.PinState{}) {
		t.Fatalf("Current(no facts) = %+v, %v; want the zero state", got, err)
	}
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3), fact(2, domain.PinOpPin, 5), fact(3, domain.PinOpUnpin, 3))
	got, err := p.Current(t.Context(), room)
	if want := (domain.PinState{Pins: []domain.Pin{pinned(2, 5)}, Version: 3}); err != nil || !same(got, want) {
		t.Fatalf("Current = %+v, %v; want %+v", got, err, want)
	}
	if s := storedState(t, rooms); s.Version != 0 {
		t.Fatalf("stored state = %+v, Current must not write", s)
	}
}

func TestProjectWritesTheFoldUpToTheTarget(t *testing.T) {
	rooms, pins := newRooms(t), memstore.NewPins()
	p := projector(t, pins, rooms)
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3), fact(2, domain.PinOpPin, 5))
	want := domain.PinState{Pins: []domain.Pin{pinned(2, 5), pinned(1, 3)}, Version: 2}
	if got, err := p.Project(t.Context(), room, 2); err != nil || !same(got, want) || !same(storedState(t, rooms), want) {
		t.Fatalf("Project(2) = %+v, %v; stored %+v; want %+v", got, err, storedState(t, rooms), want)
	}
	appendFacts(t, pins, fact(3, domain.PinOpUnpin, 5))
	want = domain.PinState{Pins: []domain.Pin{pinned(1, 3)}, Version: 3}
	if got, err := p.Project(t.Context(), room, 3); err != nil || !same(got, want) || !same(storedState(t, rooms), want) {
		t.Fatalf("Project(3) = %+v, %v; want %+v", got, err, want)
	}
}

func TestProjectAtOrPastTheTargetWritesNothing(t *testing.T) {
	rooms, pins := &countingRooms{Rooms: newRooms(t)}, memstore.NewPins()
	p := projector(t, pins, rooms)
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3), fact(2, domain.PinOpPin, 5))
	for _, target := range []uint64{2, 1, 2} {
		if got, err := p.Project(t.Context(), room, target); err != nil || got.Version != 2 {
			t.Fatalf("Project(%d) = %+v, %v; want version 2", target, got, err)
		}
	}
	if rooms.applies != 1 {
		t.Fatalf("ApplyPins ran %d times, want once", rooms.applies)
	}
}

func TestProjectIsStaleBeforeTheTargetFactIsVisible(t *testing.T) {
	rooms, pins := newRooms(t), memstore.NewPins()
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3))
	_, err := projector(t, pins, rooms).Project(t.Context(), room, 2)
	if !errors.Is(err, store.ErrStaleRead) || storedState(t, rooms).Version != 0 {
		t.Fatalf("Project(2) with one fact = %v, stored %+v; want ErrStaleRead and no write", err, storedState(t, rooms))
	}
}

func TestProjectReturnsContendedAfterALostCASAndTheNextCallSeesTheRival(t *testing.T) {
	rooms, pins := newRooms(t), memstore.NewPins()
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3))
	p := projector(t, pins, &rival{Rooms: rooms})
	if _, err := p.Project(t.Context(), room, 1); !errors.Is(err, pinproj.ErrContended) || !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("Project after a rival write = %v, want ErrContended at once", err)
	}
	got, err := p.Project(t.Context(), room, 1)
	if want := (domain.PinState{Pins: []domain.Pin{pinned(1, 3)}, Version: 1}); err != nil || !same(got, want) {
		t.Fatalf("Project again = %+v, %v; want the rival's state %+v", got, err, want)
	}
}

func TestProjectTriesTheCASOnce(t *testing.T) {
	rooms, pins := &countingRooms{Rooms: newRooms(t), lose: 1}, memstore.NewPins()
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3))
	p := projector(t, pins, rooms)
	if _, err := p.Project(t.Context(), room, 1); !errors.Is(err, pinproj.ErrContended) || rooms.applies != 1 || storedState(t, rooms).Version != 0 {
		t.Fatalf("Project = %v after %d CAS tries, stored %+v; want ErrContended after one try and no write", err, rooms.applies, storedState(t, rooms))
	}
	want := domain.PinState{Pins: []domain.Pin{pinned(1, 3)}, Version: 1}
	if got, err := p.Project(t.Context(), room, 1); err != nil || !same(got, want) || rooms.applies != 2 {
		t.Fatalf("Project again = %+v, %v after %d tries; want %+v after 2", got, err, rooms.applies, want)
	}
}

func TestCurrentPagesThroughMoreThanOneScan(t *testing.T) {
	rooms, pins := newRooms(t), memstore.NewPins()
	for pv := uint64(1); pv <= store.MaxPinScan+1; pv++ {
		op := domain.PinOpPin
		if pv%2 == 0 {
			op = domain.PinOpUnpin
		}
		appendFacts(t, pins, fact(pv, op, 3))
	}
	got, err := projector(t, pins, rooms).Current(t.Context(), room)
	if want := (domain.PinState{Pins: []domain.Pin{pinned(store.MaxPinScan+1, 3)}, Version: store.MaxPinScan + 1}); err != nil || !same(got, want) {
		t.Fatalf("Current over %d facts = %+v, %v; want %+v", store.MaxPinScan+1, got, err, want)
	}
}

func TestMissingRoomAndMissingDeps(t *testing.T) {
	p := projector(t, memstore.NewPins(), memstore.NewRooms())
	if _, err := p.Current(t.Context(), room); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("Current(missing room) = %v, want ErrRoomNotFound", err)
	}
	if _, err := p.Project(t.Context(), room, 1); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("Project(missing room) = %v, want ErrRoomNotFound", err)
	}
	if _, err := pinproj.New(nil, memstore.NewRooms()); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil facts) = %v, want ErrInvalidArgument", err)
	}
	if _, err := pinproj.New(memstore.NewPins(), nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil rooms) = %v, want ErrInvalidArgument", err)
	}
}
