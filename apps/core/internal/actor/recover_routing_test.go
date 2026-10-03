package actor_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func recoverAsync(ctx context.Context, r *actor.Router, room, from uint64) <-chan error {
	out := make(chan error, 1)
	go func() { out <- r.Recover(ctx, room, from) }()
	return out
}

func TestRecoverWaitsForTheInFlightGroupAndRepublishesItsWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		rg.sub.hold()
		sent := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "a1"))
		synctest.Wait()
		recovered := recoverAsync(t.Context(), rg.Router, roomA, 0)
		synctest.Wait()
		select {
		case err := <-recovered:
			t.Fatalf("Recover returned %v while a write group was in flight", err)
		default:
		}
		rg.sub.open()
		if r := <-sent; r.err != nil {
			t.Fatalf("Send: %v", r.err)
		}
		if err := <-recovered; err != nil {
			t.Fatalf("Recover: %v", err)
		}
		expectCalls(t, rg, roomA, "events[1]", "events[1]")
		rg.cancel()
		_ = rg.wait()
	})
}

func TestRecoverIsTurnedAwayWhenTheMailboxIsFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := baseConfig
		cfg.Mailbox = 1
		rg := started(t, cfg)
		rg.sub.hold()
		first := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "a1"))
		synctest.Wait()
		queued := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "a2"))
		synctest.Wait()
		expectErr(t, rg.Recover(t.Context(), roomA, 0), domain.ErrBusy)
		rg.sub.open()
		for _, w := range []<-chan sendResult{first, queued} {
			if r := <-w; r.err != nil {
				t.Fatalf("Send: %v", r.err)
			}
		}
		rg.cancel()
		_ = rg.wait()
	})
}

func TestRecoverIsTurnedAwayByARetiringActor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		rg.sub.hold()
		sent := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "a1"))
		synctest.Wait()
		evicted := evictAsync(t.Context(), rg.Router, roomA)
		synctest.Wait()
		expectErr(t, rg.Recover(t.Context(), roomA, 0), domain.ErrRetryLater)
		rg.sub.open()
		<-sent
		<-evicted
		expectCalls(t, rg, roomA, "events[1]")
		rg.cancel()
		_ = rg.wait()
	})
}

func TestRecoverAfterCloseIsRefusedWithoutCreatingAnActor(t *testing.T) {
	rg := started(t, baseConfig)
	seed(t, rg.msgs, roomA, time.Now(), 1)
	if err := rg.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	expectErr(t, rg.Recover(t.Context(), roomA, 0), domain.ErrRetryLater)
	if n := rg.ActorCount(); n != 0 {
		t.Fatalf("%d actors after a refused recovery, want 0", n)
	}
	expectCalls(t, rg, roomA)
}

func TestRecoverReportsWhenThePublisherRefusesTheEvents(t *testing.T) {
	rg := started(t, baseConfig)
	seed(t, rg.msgs, roomA, time.Now(), 1, 2)
	errFull := errors.New("queue full")
	rg.events.fail(errFull)
	expectErr(t, rg.Recover(t.Context(), roomA, 0), errFull)
	rg.events.fail(nil)
	mustRecover(t, rg.Router, roomA, 0)
	expectCalls(t, rg, roomA, "events[1 2]")
}

func TestRecoverRejectsAMissingRoomIDAndUnknownRooms(t *testing.T) {
	rg := started(t, baseConfig)
	expectErr(t, rg.Recover(t.Context(), 0, 0), apperr.ErrInvalidArgument)
	expectErr(t, rg.Recover(t.Context(), 999, 0), domain.ErrRoomNotFound)
}

func TestRecoverHonoursACancelledContext(t *testing.T) {
	rg := started(t, baseConfig)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	expectErr(t, rg.Recover(ctx, roomA, 0), context.Canceled)
}
