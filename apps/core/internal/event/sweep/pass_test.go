package sweep_test

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestDocNewerThanMinAgeWaitsForALaterPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		rg.createRoom(t, room, time.Now().Add(-time.Minute))
		rg.send(t, room, 1, time.Now().Add(-minAge+time.Second))
		rg.send(t, room, 2, time.Now().Add(-minAge-time.Second))
		rg.start()
		expectIDs(t, rg, pbconv.RoomCreatedEventID(room), pbconv.MessageEventID(room, 0, 2))
		expectPending(t, rg, store.StateMessages, 1)
		expectCheckpoint(t, rg, store.StateMessages, time.Now().Add(-minAge))
		rg.nextPass()
		expectIDs(t, rg, pbconv.RoomCreatedEventID(room), pbconv.MessageEventID(room, 0, 1), pbconv.MessageEventID(room, 0, 2))
		expectPending(t, rg, store.StateMessages, 0)
		rg.stop(t)
	})
}

func TestPublishFailureKeepsTheFlagAndTheCheckpointUntilALaterPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		rg.send(t, room, 1, old.Add(time.Second))
		rg.send(t, room, 2, old.Add(2*time.Second))
		rg.js.RefuseWhen(func(m *nats.Msg) error {
			if m.Header.Get("Nats-Msg-Id") == pbconv.MessageEventID(room, 0, 2) {
				return errNATS
			}
			return nil
		})
		rg.start()
		expectIDs(t, rg, pbconv.RoomCreatedEventID(room), pbconv.MessageEventID(room, 0, 1))
		expectPending(t, rg, store.StateMessages, 1)
		expectCheckpoint(t, rg, store.StateMessages, old.Add(2*time.Second))
		expectCheckpoint(t, rg, store.StateRooms, time.Now().Add(-minAge))
		if rg.PublishFailures() != 1 {
			t.Fatalf("PublishFailures = %d, want 1", rg.PublishFailures())
		}
		rg.js.RefuseWhen(nil)
		rg.nextPass()
		expectPending(t, rg, store.StateMessages, 0)
		expectCheckpoint(t, rg, store.StateMessages, time.Now().Add(-minAge))
		rg.stop(t)
	})
}

func TestUnansweredPubAcksFailAfterTheAckTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		rg.send(t, room, 1, old)
		rg.js.Hold()
		rg.start()
		if rg.Passes() != 0 || rg.js.Held() != 1 {
			t.Fatalf("passes %d, held %d; want the pass waiting on one PubAck", rg.Passes(), rg.js.Held())
		}
		time.Sleep(setup.AckTimeout)
		synctest.Wait()
		if rg.PublishFailures() != 1 {
			t.Fatalf("PublishFailures = %d, want 1", rg.PublishFailures())
		}
		expectPending(t, rg, store.StateMessages, 1)
		expectCheckpoint(t, rg, store.StateMessages, old)
		rg.js.Release()
		rg.nextPass()
		expectPending(t, rg, store.StateMessages, 0)
		expectPending(t, rg, store.StateRooms, 0)
		rg.stop(t)
	})
}

func TestRebuildFailureKeepsTheFlagAndHoldsTheCheckpoint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var broken atomic.Bool
		broken.Store(true)
		rg := newRig(t, setup, func(r sweep.Rebuilder) sweep.Rebuilder {
			return rebuildFunc(func(ctx context.Context, d store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
				if broken.Load() && d.Collection == store.StateMessages && d.Seq == 2 {
					return nil, errStore
				}
				return r.Rebuild(ctx, d, bit)
			})
		})
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		rg.send(t, room, 1, old)
		rg.send(t, room, 2, old.Add(time.Second))
		rg.send(t, room, 3, old.Add(2*time.Second))
		rg.start()
		expectIDs(t, rg, pbconv.RoomCreatedEventID(room), pbconv.MessageEventID(room, 0, 1), pbconv.MessageEventID(room, 0, 3))
		expectCheckpoint(t, rg, store.StateMessages, old.Add(time.Second))
		if rg.Failures() != 1 || rg.Dropped(store.StateMessages) != 0 {
			t.Fatalf("failures %d, dropped %d; want 1 and 0", rg.Failures(), rg.Dropped(store.StateMessages))
		}
		broken.Store(false)
		rg.nextPass()
		expectPending(t, rg, store.StateMessages, 0)
		rg.stop(t)
	})
}

func TestFirstPassOfALeadScansFromZeroAndWarnsAboutStaleState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		ancient := time.Now().Add(-10 * time.Minute)
		rg.createRoom(t, room, ancient)
		rg.send(t, room, 1, ancient)
		if err := rg.marks.Advance(t.Context(), store.StateMessages, time.Now().Add(-time.Second)); err != nil {
			t.Fatalf("Advance: %v", err)
		}
		rg.start()
		expectIDs(t, rg, pbconv.RoomCreatedEventID(room), pbconv.MessageEventID(room, 0, 1))
		if n := rg.sink.Count(staleMsg); n != 1 {
			t.Fatalf("%q logged %d times, want 1", staleMsg, n)
		}

		rg.send(t, room, 2, ancient)
		rg.nextPass()
		expectPending(t, rg, store.StateMessages, 1)

		rg.owner.leading.Store(false)
		rg.nextPass()
		rg.owner.leading.Store(true)
		time.Sleep(tick)
		synctest.Wait()
		expectPending(t, rg, store.StateMessages, 0)
		expectIDs(t, rg, pbconv.RoomCreatedEventID(room), pbconv.MessageEventID(room, 0, 1), pbconv.MessageEventID(room, 0, 2))
		rg.stop(t)
	})
}

func TestFailureBeforeTheWindowKeepsTheNextPassScanningFromZero(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		ancient := time.Now().Add(-10 * time.Minute)
		rg.createRoom(t, room, ancient)
		rg.send(t, room, 1, ancient)
		if err := rg.marks.Advance(t.Context(), store.StateMessages, time.Now().Add(-time.Second)); err != nil {
			t.Fatalf("Advance: %v", err)
		}
		rg.js.RefuseWhen(func(*nats.Msg) error { return errNATS })
		rg.start()
		expectPending(t, rg, store.StateMessages, 1)
		rg.js.RefuseWhen(nil)
		rg.nextPass()
		expectPending(t, rg, store.StateMessages, 0)
		rg.stop(t)
	})
}
