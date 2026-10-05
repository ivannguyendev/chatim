package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMsgCreatedDeclaresItsPolicy(t *testing.T) {
	e := newMsgRig(t, nil).eff.Effect()
	if e.Name != effects.MessageCreatedName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.MessageCreatedName, delay)
	}
}

func TestMsgCreatedRepublishesOnlyUnmarkedMessagesLikeTheFastPath(t *testing.T) {
	rg := newMsgRig(t, nil)
	inserted := rg.insert(t, 1, 2, 3)
	rg.marks.mark(store.MsgKey{Room: room, Seq: 2})
	if errs := rg.run(t.Context(), msgRecs(room, 1, 2, 3)); !allNil(errs, 3) {
		t.Fatalf("errs = %v, want 3 nils", errs)
	}
	want := []string{pbconv.MessageEventID(room, 0, 1), pbconv.MessageEventID(room, 0, 3)}
	if got := storedEventIDs(rg.js); !slices.Equal(got, want) {
		t.Fatalf("stored = %v, want %v", got, want)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.room.4242.msg_created" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if !proto.Equal(events[0], pbconv.MessageCreated(domain.RoomGroup, inserted[0])) || !proto.Equal(events[1], pbconv.MessageCreated(domain.RoomGroup, inserted[2])) {
		t.Fatalf("events = %v, want the fast path events for seq 1 and 3", events)
	}
	if rg.eff.Republished() != 2 || rg.eff.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 2 and 0", rg.eff.Republished(), rg.eff.Dropped())
	}
}

func TestMsgCreatedPublishesEverythingWhenMarksFail(t *testing.T) {
	rg := newMsgRig(t, nil)
	rg.insert(t, 1)
	rg.marks.mark(store.MsgKey{Room: room, Seq: 1})
	rg.marks.fail(errBoom)
	if errs := rg.run(t.Context(), msgRecs(room, 1)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MessageEventID(room, 0, 1)}) {
		t.Fatalf("stored = %v, want seq 1 despite its mark", got)
	}
}

func TestMsgCreatedDropsMissingMessagesAndUnknownRooms(t *testing.T) {
	rg := newMsgRig(t, nil)
	rg.insert(t, 1)
	recs := append(msgRecs(room, 1, 9), msgRecs(999, 1, 2)...)
	if errs := rg.run(t.Context(), recs); !allNil(errs, 4) {
		t.Fatalf("errs = %v, want nil for dropped records so they are not retried", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MessageEventID(room, 0, 1)}) {
		t.Fatalf("stored = %v, want only seq 1", got)
	}
	if rg.eff.Dropped() != 3 {
		t.Fatalf("dropped = %d, want 1 missing doc + 2 of an unknown room", rg.eff.Dropped())
	}
}

func TestMsgCreatedFailsOnlyTheRefusedRecord(t *testing.T) {
	rg := newMsgRig(t, nil)
	rg.insert(t, 1, 2, 3)
	refused := pbconv.MessageEventID(room, 0, 2)
	rg.js.RefuseWhen(func(m *nats.Msg) error {
		if publishtest.MsgID(m) == refused {
			return errBoom
		}
		return nil
	})
	errs := rg.run(t.Context(), msgRecs(room, 1, 2, 3))
	if len(errs) != 3 || errs[0] != nil || !errors.Is(errs[1], errBoom) || errs[2] != nil {
		t.Fatalf("errs = %v, want only the second record failed", errs)
	}
	if rg.eff.Republished() != 2 {
		t.Fatalf("republished = %d, want 2", rg.eff.Republished())
	}
}

func TestMsgCreatedRetriesWhenTheStoreFails(t *testing.T) {
	rg := newMsgRig(t, brokenStore{})
	errs := rg.run(t.Context(), msgRecs(room, 1, 2))
	if len(errs) != 2 || !errors.Is(errs[0], errBoom) || !errors.Is(errs[1], errBoom) || rg.eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want both failed for a retry and none dropped", errs, rg.eff.Dropped())
	}
}

func TestMsgCreatedWaitsForThePubAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newMsgRig(t, nil)
		rg.insert(t, 1)
		rg.js.Hold()
		done := make(chan []error, 1)
		go func() { done <- rg.run(context.Background(), msgRecs(room, 1)) }()
		synctest.Wait()
		select {
		case errs := <-done:
			t.Fatalf("Run returned %v before the PubAck", errs)
		default:
		}
		rg.js.Release()
		if errs := <-done; !allNil(errs, 1) || rg.eff.Republished() != 1 {
			t.Fatalf("errs = %v, republished %d; want success after the PubAck", errs, rg.eff.Republished())
		}
	})
}

func TestMsgCreatedFailsUnackedRecordsWhenCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newMsgRig(t, nil)
		rg.insert(t, 1)
		rg.js.Hold()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan []error, 1)
		go func() { done <- rg.run(ctx, msgRecs(room, 1)) }()
		synctest.Wait()
		cancel()
		if errs := <-done; len(errs) != 1 || !errors.Is(errs[0], context.Canceled) || rg.eff.Republished() != 0 {
			t.Fatalf("errs = %v, republished %d; want context.Canceled and nothing counted", errs, rg.eff.Republished())
		}
	})
}

func TestMsgCreatedCachesTheRoomType(t *testing.T) {
	rg := newMsgRig(t, nil)
	rg.insert(t, 1, 2)
	for _, s := range []uint64{1, 2} {
		if errs := rg.run(t.Context(), msgRecs(room, s)); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	if got := rg.rooms.gets.Load(); got != 1 {
		t.Fatalf("room reads = %d, want 1", got)
	}
}

func TestNewMessageCreatedRejectsMissingDeps(t *testing.T) {
	js := &publishtest.JetStream{}
	full := effects.MessageCreatedDeps{Marks: &marks{}, Messages: memstore.NewMessages(), Rooms: memstore.NewRooms(), JS: js}
	cfg := effects.MessageCreatedConfig{SubjectRoot: "evt"}
	for name, deps := range map[string]effects.MessageCreatedDeps{
		"no marks":    {Messages: full.Messages, Rooms: full.Rooms, JS: js},
		"no messages": {Marks: full.Marks, Rooms: full.Rooms, JS: js},
		"no rooms":    {Marks: full.Marks, Messages: full.Messages, JS: js},
		"no js":       {Marks: full.Marks, Messages: full.Messages, Rooms: full.Rooms},
	} {
		if _, err := effects.NewMessageCreated(deps, cfg); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewMessageCreated = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := effects.NewMessageCreated(full, effects.MessageCreatedConfig{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewMessageCreated without a subject root = %v, want ErrInvalidArgument", err)
	}
	eff, err := effects.NewMessageCreated(full, cfg)
	if err != nil {
		t.Fatalf("NewMessageCreated with defaults: %v", err)
	}
	if got := eff.Effect().Delay; got != effects.DefaultDelay {
		t.Fatalf("default delay = %v, want %v", got, effects.DefaultDelay)
	}
}
