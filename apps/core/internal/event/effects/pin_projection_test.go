package effects_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestPinProjectionDeclaresItsPolicy(t *testing.T) {
	e := newReactRig(t).pinProj.Effect()
	if e.Name != effects.PinProjectionName || e.Delay != 0 || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay 0", e.Name, e.Delay, effects.PinProjectionName)
	}
}

func TestPinProjectionFoldsEachRoomOnceUpToItsNewestFact(t *testing.T) {
	rg := newReactRig(t)
	createRoom(t, rg.rooms, otherRoom)
	rg.pin(t, room, 1, domain.PinOpPin, 1)
	rg.pin(t, room, 2, domain.PinOpPin, 2)
	rg.pin(t, otherRoom, 1, domain.PinOpPin, 5)
	recs := []work.Record{pinRec(room, 1), pinRec(otherRoom, 1), pinRec(room, 2), pinRec(999, 1)}
	if errs := rg.pinProj.Effect().Run(t.Context(), recs); !allNil(errs, 4) {
		t.Fatalf("errs = %v", errs)
	}
	want := []projectCall{{room: room, target: 2}, {room: otherRoom, target: 1}, {room: 999, target: 1}}
	if !slices.Equal(rg.projects.calls, want) {
		t.Fatalf("projects = %+v, want one per room up to its newest pin version", rg.projects.calls)
	}
	state, err := rg.rooms.PinState(t.Context(), room)
	if err != nil || state.Version != 2 || len(state.Pins) != 2 || state.Pins[0].Seq != 2 {
		t.Fatalf("projection = %+v, %v; want version 2 with seq 2 first", state, err)
	}
	if rg.pinProj.Dropped() != 1 {
		t.Fatalf("dropped %d, want 1 for the unknown room", rg.pinProj.Dropped())
	}
}

func TestPinProjectionRetriesOnlyTheRoomThatFailed(t *testing.T) {
	rg := newReactRig(t)
	createRoom(t, rg.rooms, otherRoom)
	rg.pin(t, room, 1, domain.PinOpPin, 1)
	rg.pin(t, otherRoom, 1, domain.PinOpPin, 5)
	rg.projects.fail = map[uint64]error{room: errBoom}
	errs := rg.pinProj.Effect().Run(t.Context(), []work.Record{pinRec(room, 1), pinRec(otherRoom, 1), pinRec(room, 1)})
	if len(errs) != 3 || !errors.Is(errs[0], errBoom) || errs[1] != nil || !errors.Is(errs[2], errBoom) || rg.pinProj.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want both records of the failed room retried", errs, rg.pinProj.Dropped())
	}
}
