package actor_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestIdleActorIsEvictedAndReloadedOnNextSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := baseConfig
		cfg.Idle = 10 * time.Second
		rg := started(t, cfg)
		first := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		time.Sleep(cfg.Idle - time.Millisecond)
		mustSend(t, rg.Router, cmd(roomA, "alice", "c2"))
		time.Sleep(cfg.Idle - time.Millisecond)
		synctest.Wait()
		if n := rg.ActorCount(); n != 1 {
			t.Fatalf("actor evicted %v after its last message, want %v idle", cfg.Idle-time.Millisecond, cfg.Idle)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("%d actors after %v idle, want 0", n, cfg.Idle)
		}
		if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c3")); ack.Seq != 3 {
			t.Fatalf("reloaded actor assigned seq %d, want 3", ack.Seq)
		}
		if again := mustSend(t, rg.Router, cmd(roomA, "alice", "c1")); !sameAck(again, first) {
			t.Fatalf("reloaded actor answered c1 with %+v, want seeded %+v", again, first)
		}
		if lasts, pages, _ := rg.msgs.counts(); lasts != 2 || pages != 2 {
			t.Fatalf("Last %d, Page %d, want one load per actor", lasts, pages)
		}
	})
}

func TestSendRacingIdleEvictionIsNeverLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := baseConfig
		cfg.Idle = time.Second
		rg := started(t, cfg)
		ctx := t.Context()
		sent := 0
		for round := range 30 {
			mustSend(t, rg.Router, cmd(roomA, "alice", fmt.Sprintf("warm-%d", round)))
			time.Sleep(cfg.Idle)
			var waits []<-chan sendResult
			for i := range round%3 + 1 {
				waits = append(waits, sendAsync(ctx, rg.Router, cmd(roomA, "bob", fmt.Sprintf("race-%d-%d", round, i))))
			}
			for _, w := range waits {
				if got := <-w; got.err != nil {
					t.Fatalf("round %d: send racing eviction failed: %v", round, got.err)
				}
			}
			sent += len(waits) + 1
		}
		docs := timeline(t, rg.msgs.Messages, roomA)
		if len(docs) != sent || docs[len(docs)-1].Seq != uint64(sent) {
			t.Fatalf("stored %d messages ending at seq %d, want %d", len(docs), docs[len(docs)-1].Seq, sent)
		}
	})
}

func TestCloseDrainsQueuedAndInFlightCommands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		ctx := t.Context()
		waits := []<-chan sendResult{sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1"))}
		synctest.Wait()
		waits = append(waits,
			sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c2")),
			sendAsync(ctx, rg.Router, cmd(roomB, "bob", "c3")))
		synctest.Wait()

		closed := make(chan error, 1)
		go func() { closed <- rg.Close(context.Background()) }()
		synctest.Wait()
		select {
		case err := <-closed:
			t.Fatalf("Close returned %v with work in flight", err)
		default:
		}
		_, err := rg.Send(ctx, cmd(roomA, "alice", "late"))
		expectErr(t, err, domain.ErrRetryLater)

		rg.sub.open()
		for i, w := range waits {
			if got := <-w; got.err != nil {
				t.Fatalf("command %d dropped by Close: %v", i, got.err)
			}
		}
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if err := rg.wait(); err != nil {
			t.Fatalf("Run after Close = %v, want nil", err)
		}
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("%d actors left after Close", n)
		}
	})
}

func TestCloseRespectsItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		w := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "c1"))
		synctest.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		expectErr(t, rg.Close(ctx), context.DeadlineExceeded)
		rg.sub.open()
		if got := <-w; got.err != nil {
			t.Fatalf("in-flight command lost after Close timed out: %v", got.err)
		}
		if err := rg.wait(); err != nil {
			t.Fatalf("Run = %v, want nil once drained", err)
		}
	})
}

func TestHardStopAnswersEveryoneWithRetryLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		ctx := t.Context()
		waits := []<-chan sendResult{
			sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1")),
			sendAsync(ctx, rg.Router, cmd(roomB, "bob", "c2")),
		}
		synctest.Wait()
		waits = append(waits, sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c3")))
		synctest.Wait()

		rg.cancel()
		for _, w := range waits {
			expectErr(t, (<-w).err, domain.ErrRetryLater)
		}
		expectErr(t, rg.wait(), context.Canceled)
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("%d actors left after hard stop", n)
		}
		_, err := rg.Send(ctx, cmd(roomA, "alice", "c4"))
		expectErr(t, err, domain.ErrRetryLater)
	})
}

func TestSendOutsideRunIsRetryLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if err := rg.Close(t.Context()); err != nil {
			t.Fatalf("Close before Run = %v, want nil", err)
		}
		if err := rg.Run(t.Context()); err != nil {
			t.Fatalf("Run after Close = %v, want nil", err)
		}
		_, err = rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if err := rg.Run(t.Context()); err == nil || errors.Is(err, context.Canceled) {
			t.Fatalf("second Run = %v, want an already-started error", err)
		}
	})
}
