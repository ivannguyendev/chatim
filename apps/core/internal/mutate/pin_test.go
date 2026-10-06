package mutate_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func pinCmd(user string, seq uint64) mutate.PinCmd {
	return mutate.PinCmd{Tenant: tenant, User: user, Room: room, Seq: seq}
}

func pinnedSeqs(s domain.PinState) []uint64 {
	out := make([]uint64, len(s.Pins))
	for i, p := range s.Pins {
		out[i] = p.Seq
	}
	return out
}

func samePin(a, b domain.PinAction) bool {
	at := a.At.Equal(b.At)
	a.At, b.At = time.Time{}, time.Time{}
	return at && a == b
}

func (rg *rig) pinFacts(t *testing.T) []domain.PinAction {
	t.Helper()
	got, err := rg.pins.After(t.Context(), room, 0, store.MaxPinScan)
	if err != nil {
		t.Fatalf("pin facts: %v", err)
	}
	return got
}

func (rg *rig) mustPin(t *testing.T, pin bool, c mutate.PinCmd) domain.PinState {
	t.Helper()
	call := rg.m.Pin
	if !pin {
		call = rg.m.Unpin
	}
	got, err := call(t.Context(), c)
	if err != nil {
		t.Fatalf("pin=%v by %s on seq %d: %v", pin, c.User, c.Seq, err)
	}
	return got
}

func TestPinAppendsAFactAndProjectsItBeforeTheAck(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	got := rg.mustPin(t, true, pinCmd("bob", 1))
	want := domain.PinAction{Room: room, PV: 1, Tenant: tenant, Op: domain.PinOpPin, Seq: 1, By: "bob", At: rg.at()}
	facts := rg.pinFacts(t)
	if len(facts) != 1 || !samePin(facts[0], want) {
		t.Fatalf("facts = %+v, want %+v", facts, want)
	}
	if got.Version != 1 || len(got.Pins) != 1 || got.Pins[0].Seq != 1 || got.Pins[0].By != "bob" || got.Pins[0].PV != 1 || !got.Pins[0].At.Equal(rg.at()) {
		t.Fatalf("state = %+v, want seq 1 pinned by bob at version 1", got)
	}
	if stored, err := rg.rooms.PinState(t.Context(), room); err != nil || stored.Version != 1 || !slices.Equal(pinnedSeqs(stored), []uint64{1}) {
		t.Fatalf("projection = %+v, %v; want version 1 written before the ack", stored, err)
	}
	rooms, events := rg.events.list()
	if !slices.Equal(rooms, []uint64{room}) || len(events) != 1 || !proto.Equal(events[0], pbconv.MessagePinned(domain.RoomGroup, rg.stored(t, 1), facts[0])) {
		t.Fatalf("enqueued %v %v, want one msg_pinned", rooms, events)
	}
	if id := events[0].GetId(); id != pbconv.PinEventID(room, 1) {
		t.Fatalf("event id = %q", id)
	}
}

func TestPinAndUnpinInTheWantedStateWriteNothing(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.send(t, 2, "alice", "ho")
	rg.mustPin(t, true, pinCmd("bob", 1))
	rg.now = rg.now.Add(time.Second)
	noops := []struct {
		pin bool
		cmd mutate.PinCmd
	}{{true, pinCmd("carol", 1)}, {false, pinCmd("bob", 2)}}
	for _, n := range noops {
		if got := rg.mustPin(t, n.pin, n.cmd); got.Version != 1 || !slices.Equal(pinnedSeqs(got), []uint64{1}) {
			t.Fatalf("no-op pin=%v on seq %d = %+v, want version 1 with seq 1", n.pin, n.cmd.Seq, got)
		}
	}
	if facts := rg.pinFacts(t); len(facts) != 1 {
		t.Fatalf("no-ops wrote facts %+v", facts)
	}
	if got := rg.mustPin(t, false, pinCmd("carol", 1)); got.Version != 2 || len(got.Pins) != 0 {
		t.Fatalf("unpin = %+v, want version 2 without pins", got)
	}
	if again := rg.mustPin(t, false, pinCmd("carol", 1)); again.Version != 2 || len(again.Pins) != 0 {
		t.Fatalf("second unpin = %+v, want a no-op at version 2", again)
	}
	_, events := rg.events.list()
	if len(events) != 2 || events[1].GetMessageUnpinned() == nil || events[1].GetId() != pbconv.PinEventID(room, 2) {
		t.Fatalf("events = %v, want msg_pinned then msg_unpinned v2 only", events)
	}
	if facts := rg.pinFacts(t); len(facts) != 2 {
		t.Fatalf("facts = %+v, want the pin and one unpin", facts)
	}
}

func TestThePinLimitIsExact(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	d.Limits = mutate.Limits{PinLimit: 2}
	rg.m = rg.build(t, d)
	for _, seq := range []uint64{1, 2, 3} {
		rg.send(t, seq, "alice", "m")
	}
	rg.mustPin(t, true, pinCmd("bob", 1))
	rg.mustPin(t, true, pinCmd("bob", 2))
	if _, err := rg.m.Pin(t.Context(), pinCmd("bob", 3)); !errors.Is(err, domain.ErrTooManyPins) || !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("third pin = %v, want ErrTooManyPins", err)
	}
	if again := rg.mustPin(t, true, pinCmd("carol", 1)); again.Version != 2 {
		t.Fatalf("re-pin at the limit = %+v, want a no-op at version 2", again)
	}
	rg.mustPin(t, false, pinCmd("bob", 2))
	got := rg.mustPin(t, true, pinCmd("bob", 3))
	if got.Version != 4 || !slices.Equal(pinnedSeqs(got), []uint64{3, 1}) {
		t.Fatalf("state = %+v, want version 4 with seq 3 then seq 1", got)
	}
}

func TestPinningADeletedMessageFailsButUnpinningWorks(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.send(t, 2, "alice", "ho")
	rg.mustPin(t, true, pinCmd("bob", 1))
	for _, seq := range []uint64{1, 2} {
		if _, err := rg.m.Delete(t.Context(), del("alice", seq, 0)); err != nil {
			t.Fatalf("Delete seq %d: %v", seq, err)
		}
	}
	if _, err := rg.m.Pin(t.Context(), pinCmd("bob", 2)); !errors.Is(err, domain.ErrMessageDeleted) {
		t.Fatalf("pin a deleted message = %v, want ErrMessageDeleted", err)
	}
	if stored, err := rg.rooms.PinState(t.Context(), room); err != nil || !slices.Equal(pinnedSeqs(stored), []uint64{1}) {
		t.Fatalf("pins after delete = %+v, %v; want seq 1 kept", stored, err)
	}
	if got := rg.mustPin(t, false, pinCmd("bob", 1)); got.Version != 2 || len(got.Pins) != 0 {
		t.Fatalf("unpin a deleted message = %+v, want version 2 without pins", got)
	}
	_, events := rg.events.list()
	last := events[len(events)-1].GetMessageUnpinned()
	if last == nil || !last.GetMessage().GetDeleted() || last.GetMessage().GetText() != "" {
		t.Fatalf("last event = %v, want msg_unpinned of the deleted message without text", events[len(events)-1])
	}
}
