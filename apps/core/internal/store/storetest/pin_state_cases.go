package storetest

import (
	"context"
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func pinStateCases() []pinCase {
	return []pinCase{
		{"pin state of a new room is empty and of a missing room not found", pinStateEmpty},
		{"apply pins writes only over the expected version", pinStateCAS},
		{"apply pins needs a version above the base", pinStateInvalid},
		{"pins never change the room read", pinStateRoomUnchanged},
		{"cancelled context writes nothing", pinsCancelled},
	}
}

func pinStateEmpty(t *testing.T, s pinStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	assertPinState(t, s.rooms, roomA, domain.PinState{})
	_, err := s.rooms.PinState(t.Context(), roomB)
	assertErrorIs(t, "PinState(missing room)", err, domain.ErrRoomNotFound)
}

func pinStateCAS(t *testing.T, s pinStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	first := domain.PinState{Pins: []domain.Pin{pinOf(3, 1)}, Version: 1}
	mustApplyPins(t, s.rooms, roomA, 0, first, true)
	assertPinState(t, s.rooms, roomA, first)
	mustApplyPins(t, s.rooms, roomA, 0, domain.PinState{Pins: []domain.Pin{pinOf(9, 1)}, Version: 1}, false)
	mustApplyPins(t, s.rooms, roomA, 2, domain.PinState{Version: 3}, false)
	assertPinState(t, s.rooms, roomA, first)
	second := domain.PinState{Pins: []domain.Pin{pinOf(5, 2), pinOf(3, 1)}, Version: 2}
	mustApplyPins(t, s.rooms, roomA, 1, second, true)
	assertPinState(t, s.rooms, roomA, second)
	cleared := domain.PinState{Version: 3}
	mustApplyPins(t, s.rooms, roomA, 2, cleared, true)
	assertPinState(t, s.rooms, roomA, cleared)
	mustApplyPins(t, s.rooms, roomB, 0, first, false)
}

func pinStateInvalid(t *testing.T, s pinStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	for name, c := range map[string]struct {
		base uint64
		st   domain.PinState
	}{
		"same version":            {1, domain.PinState{Version: 1}},
		"lower version":           {2, domain.PinState{Version: 1}},
		"zero version":            {0, domain.PinState{}},
		"version above max int64": {0, domain.PinState{Version: math.MaxInt64 + 1}},
	} {
		_, err := s.rooms.ApplyPins(t.Context(), roomA, c.base, c.st)
		assertErrorIs(t, "ApplyPins("+name+")", err, apperr.ErrInvalidArgument)
	}
	assertPinState(t, s.rooms, roomA, domain.PinState{})
}

func pinStateRoomUnchanged(t *testing.T, s pinStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	mustApplyPins(t, s.rooms, roomA, 0, domain.PinState{Pins: []domain.Pin{pinOf(3, 1)}, Version: 1}, true)
	assertRoom(t, s.rooms, room)
}

func pinsCancelled(t *testing.T, s pinStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	ctx := cancelledContext(t)
	assertErrorIs(t, "Append", s.pins.Append(ctx, pinFact(roomA, 1, domain.PinOpPin, 3)), context.Canceled)
	_, err := s.pins.At(ctx, roomA, 1)
	assertErrorIs(t, "At", err, context.Canceled)
	_, err = s.pins.After(ctx, roomA, 0, 10)
	assertErrorIs(t, "After", err, context.Canceled)
	_, err = s.rooms.PinState(ctx, roomA)
	assertErrorIs(t, "PinState", err, context.Canceled)
	_, err = s.rooms.ApplyPins(ctx, roomA, 0, domain.PinState{Version: 1})
	assertErrorIs(t, "ApplyPins", err, context.Canceled)
	if got, err := s.pins.After(t.Context(), roomA, 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("After after a cancelled Append = %+v, %v; want nothing stored", got, err)
	}
	assertPinState(t, s.rooms, roomA, domain.PinState{})
}
