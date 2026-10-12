package effects_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
)

func TestPinEventDeclaresItsPolicy(t *testing.T) {
	e := newReactRig(t).pinEvent.Effect()
	if e.Name != effects.PinEventName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.PinEventName, delay)
	}
}

func TestPinEventPublishesPinsAndUnpinsWithTheMessage(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	pinned := rg.pin(t, room, 1, domain.PinOpPin, 1)
	unpinned := rg.pin(t, room, 2, domain.PinOpUnpin, 1)
	if errs := rg.pinEvent.Effect().Run(t.Context(), []work.Record{pinRec(room, 1), pinRec(room, 2)}); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.PinEventID(room, 1), pbconv.PinEventID(room, 2)}) {
		t.Fatalf("stored = %v", got)
	}
	stored := rg.js.Stored()
	if stored[0].Subject != "evt.acme.room.4242.msg_pinned" || stored[1].Subject != "evt.acme.room.4242.msg_unpinned" {
		t.Fatalf("subjects = %q, %q", stored[0].Subject, stored[1].Subject)
	}
	events, err := rg.js.Events()
	msg := rg.stored(t, 1)
	if err != nil || !proto.Equal(events[0], pbconv.PinChanged(domain.RoomGroup, msg, pinned)) || !proto.Equal(events[1], pbconv.PinChanged(domain.RoomGroup, msg, unpinned)) {
		t.Fatalf("events = %v, %v; want the fast path events", events, err)
	}
	if rg.pinEvent.Republished() != 2 || rg.pinEvent.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 2 and 0", rg.pinEvent.Republished(), rg.pinEvent.Dropped())
	}
}

func TestPinEventHidesTheTextOfADeletedMessage(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.pin(t, room, 1, domain.PinOpPin, 1)
	gone := domain.Edit{Room: room, Seq: 1, Version: 1, Kind: domain.EditDelete, Tenant: tenant, By: "alice", At: time.Now().UTC().Truncate(time.Millisecond)}
	if err := rg.msgs.ApplyEdit(t.Context(), gone); err != nil {
		t.Fatalf("ApplyEdit: %v", err)
	}
	if errs := rg.pinEvent.Effect().Run(t.Context(), []work.Record{pinRec(room, 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	events, err := rg.js.Events()
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %v, %v", events, err)
	}
	if m := events[0].GetMessagePinned().GetMessage(); !m.GetDeleted() || m.GetText() != "" {
		t.Fatalf("pinned message = %v, want deleted without text", m)
	}
}

func TestPinEventCountsOnlyEventsTheStreamLacked(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	a := rg.pin(t, room, 1, domain.PinOpPin, 1)
	fast, err := publish.Message("evt", room, pbconv.PinChanged(domain.RoomGroup, rg.stored(t, 1), a))
	if err != nil {
		t.Fatalf("publish.Message: %v", err)
	}
	if _, err := rg.js.PublishMsgAsync(fast); err != nil {
		t.Fatalf("fast path publish: %v", err)
	}
	if errs := rg.pinEvent.Effect().Run(t.Context(), []work.Record{pinRec(room, 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.js.Stored()) != 1 || len(rg.js.Attempts()) != 2 || rg.pinEvent.Republished() != 0 {
		t.Fatalf("stored %d, attempts %d, republished %d; want 1, 2 and 0", len(rg.js.Stored()), len(rg.js.Attempts()), rg.pinEvent.Republished())
	}
}

func TestPinEventDropsWhatIsGone(t *testing.T) {
	rg := newReactRig(t)
	rg.pin(t, room, 1, domain.PinOpPin, 7)
	rg.message(t, 999, 1)
	rg.pin(t, 999, 1, domain.PinOpPin, 1)
	if errs := rg.pinEvent.Effect().Run(t.Context(), []work.Record{pinRec(room, 9), pinRec(room, 1), pinRec(999, 1)}); !allNil(errs, 3) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.pinEvent.Dropped() != 3 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 3 (no fact, no message, no room)", len(rg.js.Attempts()), rg.pinEvent.Dropped())
	}
}

func TestPinEventRetriesWhenTheStoreFails(t *testing.T) {
	eff, err := effects.NewPinEvent(
		effects.PinEventDeps{Pins: brokenPins{}, Messages: brokenStore{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewPinEvent: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), []work.Record{pinRec(room, 1)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}
