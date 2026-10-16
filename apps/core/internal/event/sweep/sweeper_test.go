package sweep_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const stoppedMsg = "event sweeper lost the leader slot"

func TestSweepRepublishesFlaggedDocsAndClearsThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		rg.send(t, room, 1, old)
		rg.send(t, room, 2, old.Add(time.Second))
		rg.start()
		expectIDs(t, rg, pbconv.RoomCreatedEventID(room), pbconv.MessageEventID(room, 0, 1), pbconv.MessageEventID(room, 0, 2))
		expectPending(t, rg, store.StateMessages, 0)
		expectPending(t, rg, store.StateRooms, 0)
		expectCheckpoint(t, rg, store.StateMessages, time.Now().Add(-minAge))
		expectCheckpoint(t, rg, store.StateRooms, time.Now().Add(-minAge))
		if rg.Republished(store.StateMessages) != 2 || rg.Republished(store.StateRooms) != 1 || rg.Passes() != 1 {
			t.Fatalf("republished messages/rooms = %d/%d, passes %d; want 2/1/1", rg.Republished(store.StateMessages), rg.Republished(store.StateRooms), rg.Passes())
		}
		if n := rg.sink.Count(startedMsg); n != 1 {
			t.Fatalf("%q logged %d times, want 1", startedMsg, n)
		}
		rg.nextPass()
		if rg.Passes() != 2 || len(rg.js.Attempts()) != 3 {
			t.Fatalf("second pass: passes %d, attempts %d; want 2 passes and no new publish", rg.Passes(), len(rg.js.Attempts()))
		}
		rg.stop(t)
	})
}

func TestFollowerNeverSweeps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		rg.owner.leading.Store(false)
		rg.send(t, room, 1, time.Now().Add(-time.Minute))
		rg.start()
		time.Sleep(10 * tick)
		synctest.Wait()
		if len(rg.js.Attempts()) != 0 || rg.Passes() != 0 || rg.sink.Count(startedMsg) != 0 {
			t.Fatalf("follower published %d, passes %d", len(rg.js.Attempts()), rg.Passes())
		}
		rg.stop(t)
	})
}

func TestLosingTheLeadMidPageStopsAfterThePageWithoutAdvancing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := setup
		cfg.Batch = 2
		var rg *rig
		rg = newRig(t, cfg, func(r sweep.Rebuilder) sweep.Rebuilder {
			return rebuildFunc(func(ctx context.Context, d store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
				rg.owner.leading.Store(false)
				return r.Rebuild(ctx, d, bit)
			})
		})
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		for seq := uint64(1); seq <= 5; seq++ {
			rg.send(t, room, seq, old)
		}
		rg.start()
		expectIDs(t, rg, pbconv.MessageEventID(room, 0, 1), pbconv.MessageEventID(room, 0, 2))
		expectPending(t, rg, store.StateMessages, 3)
		expectPending(t, rg, store.StateRooms, 1)
		expectCheckpoint(t, rg, store.StateMessages, time.Time{})
		if rg.Passes() != 0 || rg.sink.Count(stoppedMsg) != 1 {
			t.Fatalf("passes %d, %q logged %d times; want 0 and 1", rg.Passes(), stoppedMsg, rg.sink.Count(stoppedMsg))
		}
		rg.stop(t)
	})
}

func TestLostLeadResetsTheOldestGaugeAndANewLeadStartsAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		rg.js.RefuseWhen(func(*nats.Msg) error { return errNATS })
		rg.createRoom(t, room, time.Now().Add(-time.Minute))
		rg.send(t, room, 1, time.Now().Add(-time.Minute))
		rg.start()
		if got := rg.PendingOldest(store.StateMessages); got != time.Minute {
			t.Fatalf("PendingOldest = %v, want 1m", got)
		}
		rg.owner.leading.Store(false)
		rg.nextPass()
		if got := rg.PendingOldest(store.StateMessages); got != 0 {
			t.Fatalf("PendingOldest after losing the lead = %v, want 0", got)
		}
		rg.js.RefuseWhen(nil)
		rg.owner.leading.Store(true)
		time.Sleep(tick)
		synctest.Wait()
		expectIDs(t, rg, pbconv.RoomCreatedEventID(room), pbconv.MessageEventID(room, 0, 1))
		if n := rg.sink.Count(startedMsg); n != 2 {
			t.Fatalf("%q logged %d times, want 2", startedMsg, n)
		}
		rg.stop(t)
	})
}

func TestRunTwiceAndCloseBeforeRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		rg.start()
		if err := rg.Run(context.Background()); err == nil {
			t.Fatal("second Run = nil, want an error")
		}
		rg.stop(t)

		idle := newRig(t, setup, nil)
		if err := idle.Close(context.Background()); err != nil {
			t.Fatalf("Close before Run = %v", err)
		}
	})
}

func TestRunEndsWithTheContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { rg.done <- rg.Run(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-rg.done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	})
}
