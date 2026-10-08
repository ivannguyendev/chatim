package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func newRoomCreated(t *testing.T, rooms effects.RoomReader, js *publishtest.JetStream) *effects.RoomCreated {
	t.Helper()
	eff, err := effects.NewRoomCreated(effects.RoomCreatedDeps{Rooms: rooms, JS: js}, effects.RoomCreatedConfig{SubjectRoot: "evt", Delay: delay})
	if err != nil {
		t.Fatalf("NewRoomCreated: %v", err)
	}
	return eff
}

func roomRecs(ids ...uint64) []work.Record {
	out := make([]work.Record, len(ids))
	for i, id := range ids {
		out[i] = work.Record{Kind: store.RoomInserted, Room: id, CommittedAt: time.Now()}
	}
	return out
}

func TestRoomCreatedDeclaresItsPolicy(t *testing.T) {
	e := newRoomCreated(t, memstore.NewRooms(), &publishtest.JetStream{}).Effect()
	if e.Name != effects.RoomCreatedName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.RoomCreatedName, delay)
	}
}

func TestRoomCreatedPublishesTheFastPathEvent(t *testing.T) {
	mem := memstore.NewRooms()
	r := createRoom(t, mem, room)
	js := &publishtest.JetStream{}
	eff := newRoomCreated(t, mem, js)
	if errs := eff.Effect().Run(t.Context(), roomRecs(room)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(js); !slices.Equal(got, []string{pbconv.RoomCreatedEventID(room)}) {
		t.Fatalf("stored = %v, want %s", got, pbconv.RoomCreatedEventID(room))
	}
	if subj := js.Stored()[0].Subject; subj != "evt.acme.room.4242.room_created" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := js.Events()
	if err != nil || !proto.Equal(events[0], pbconv.RoomCreated(r)) {
		t.Fatalf("event = %v, %v; want the fast path event %v", events, err, pbconv.RoomCreated(r))
	}
	if eff.Republished() != 1 || eff.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 1 and 0", eff.Republished(), eff.Dropped())
	}
}

func TestRoomCreatedDropsAnUnknownRoom(t *testing.T) {
	js := &publishtest.JetStream{}
	eff := newRoomCreated(t, memstore.NewRooms(), js)
	if errs := eff.Effect().Run(t.Context(), roomRecs(999)); !allNil(errs, 1) {
		t.Fatalf("errs = %v, want nil so the record is not retried", errs)
	}
	if len(js.Attempts()) != 0 || eff.Dropped() != 1 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 1", len(js.Attempts()), eff.Dropped())
	}
}

func TestRoomCreatedFailsOnlyTheRefusedRecord(t *testing.T) {
	mem := memstore.NewRooms()
	createRoom(t, mem, room)
	createRoom(t, mem, 777)
	js := &publishtest.JetStream{}
	js.RefuseWhen(func(m *nats.Msg) error {
		if publishtest.MsgID(m) == pbconv.RoomCreatedEventID(777) {
			return errBoom
		}
		return nil
	})
	eff := newRoomCreated(t, mem, js)
	errs := eff.Effect().Run(t.Context(), roomRecs(room, 777))
	if len(errs) != 2 || errs[0] != nil || !errors.Is(errs[1], errBoom) {
		t.Fatalf("errs = %v, want only room 777 failed", errs)
	}
}

func TestRoomCreatedRetriesWhenTheStoreFails(t *testing.T) {
	eff := newRoomCreated(t, brokenStore{}, &publishtest.JetStream{})
	if errs := eff.Effect().Run(t.Context(), roomRecs(room)); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}

func TestRoomCreatedWaitsForThePubAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mem := memstore.NewRooms()
		createRoom(t, mem, room)
		js := &publishtest.JetStream{}
		js.Hold()
		eff := newRoomCreated(t, mem, js)
		done := make(chan []error, 1)
		go func() { done <- eff.Effect().Run(context.Background(), roomRecs(room)) }()
		synctest.Wait()
		select {
		case errs := <-done:
			t.Fatalf("Run returned %v before the PubAck", errs)
		default:
		}
		js.Release()
		if errs := <-done; !allNil(errs, 1) || eff.Republished() != 1 {
			t.Fatalf("errs = %v, republished %d; want success after the PubAck", errs, eff.Republished())
		}
	})
}

func TestNewRoomCreatedRejectsBadInput(t *testing.T) {
	js := &publishtest.JetStream{}
	cfg := effects.RoomCreatedConfig{SubjectRoot: "evt"}
	for name, deps := range map[string]effects.RoomCreatedDeps{
		"no rooms": {JS: js},
		"no js":    {Rooms: memstore.NewRooms()},
	} {
		if _, err := effects.NewRoomCreated(deps, cfg); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewRoomCreated = %v, want ErrInvalidArgument", name, err)
		}
	}
	full := effects.RoomCreatedDeps{Rooms: memstore.NewRooms(), JS: js}
	if _, err := effects.NewRoomCreated(full, effects.RoomCreatedConfig{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewRoomCreated without a subject root = %v, want ErrInvalidArgument", err)
	}
	eff, err := effects.NewRoomCreated(full, cfg)
	if err != nil {
		t.Fatalf("NewRoomCreated with defaults: %v", err)
	}
	if got := eff.Effect().Delay; got != effects.DefaultDelay {
		t.Fatalf("default delay = %v, want %v", got, effects.DefaultDelay)
	}
}
