package sweep_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/interaction/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

func (f *fullRig) pin(t *testing.T, pv uint64, op domain.PinOp, seq uint64, at time.Time) {
	t.Helper()
	a := domain.PinAction{Room: room, PV: pv, Tenant: tenant, Op: op, Seq: seq, By: "ann", At: at}
	if err := f.pins.Append(t.Context(), a); err != nil {
		t.Fatalf("Append pin v%d: %v", pv, err)
	}
}

func TestPinFactsAreProjectedThenPublished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.send(t, room, 1, old)
		f.pin(t, 1, domain.PinOpPin, 1, old)
		f.pin(t, 2, domain.PinOpUnpin, 1, old.Add(time.Second))
		f.settle()
		expectEvents(t, f, pbconv.PinEventID(room, 1), pbconv.PinEventID(room, 2))
		if ev := f.published(t, pbconv.PinEventID(room, 1)); ev.GetMessagePinned().GetMessage().GetText() != "hi" {
			t.Fatalf("msg_pinned = %+v, want the message snapshot", ev)
		}
		if ev := f.published(t, pbconv.PinEventID(room, 2)); ev.GetMessageUnpinned() == nil {
			t.Fatalf("pin v2 event = %+v, want msg_unpinned", ev)
		}
		st, err := f.rooms.PinState(t.Context(), room)
		if err != nil || st.Version != 2 || len(st.Pins) != 0 {
			t.Fatalf("pin state = %+v, %v; want v2 with no pins", st, err)
		}
		expectPending(t, f.rig, store.StatePins, 0)
		f.stop(t)
	})
}

type losingRooms struct{ *memstore.Rooms }

func (losingRooms) ApplyPins(context.Context, uint64, uint64, domain.PinState) (bool, error) {
	return false, nil
}

func TestPinWithALostProjectionCASKeepsItsFlag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, func(f *fullRig) sweep.RebuilderStores {
			s := f.stores(t)
			proj, err := pinproj.New(f.pins, losingRooms{f.rooms})
			if err != nil {
				t.Fatalf("pinproj.New: %v", err)
			}
			s.PinProjector = proj
			return s
		})
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.send(t, room, 1, old)
		f.pin(t, 1, domain.PinOpPin, 1, old)
		f.settle()
		if f.event(t, pbconv.PinEventID(room, 1)) {
			t.Fatal("msg_pinned published while the projection lost its CAS")
		}
		expectPending(t, f.rig, store.StatePins, 1)
		if f.Failures() == 0 || f.Dropped(store.StatePins) != 0 {
			t.Fatalf("failures %d, dropped %d; want a failure and no drop", f.Failures(), f.Dropped(store.StatePins))
		}
		f.stop(t)
	})
}

func TestPinOfAMissingMessageIsDroppedAndCleared(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.pin(t, 1, domain.PinOpPin, 9, old)
		f.settle()
		expectPending(t, f.rig, store.StatePins, 0)
		if f.Dropped(store.StatePins) != 1 || f.event(t, pbconv.PinEventID(room, 1)) {
			t.Fatalf("dropped %d; want the pin dropped without an event", f.Dropped(store.StatePins))
		}
		f.stop(t)
	})
}
