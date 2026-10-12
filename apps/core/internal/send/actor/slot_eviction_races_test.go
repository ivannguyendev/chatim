package actor_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestEvictSlotsOutsideRunIsNoOp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.EvictSlots(t.Context(), slotsOf(roomA))
		rg.start(t)
		mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		rg.EvictSlots(t.Context(), nil)
		rg.EvictSlots(t.Context(), []uint16{65535})
		if n := rg.ActorCount(); n != 1 {
			t.Fatalf("%d actors after evicting no matching slot, want 1", n)
		}
		if err := rg.Close(t.Context()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		rg.EvictSlots(t.Context(), slotsOf(roomA))
	})
}

func TestRetireRacingIdleEvictionAnswersEveryCallerOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := baseConfig
		cfg.Idle = time.Second
		rg := started(t, cfg)
		ctx := t.Context()
		acked := 0
		for round := range 30 {
			mustSend(t, rg.Router, cmd(roomA, "alice", fmt.Sprintf("warm-%d", round)))
			acked++
			time.Sleep(cfg.Idle)
			evicted := evictAsync(ctx, rg.Router, roomA)
			var waits []<-chan sendResult
			for i := range round%3 + 1 {
				waits = append(waits, sendAsync(ctx, rg.Router, cmd(roomA, "bob", fmt.Sprintf("race-%d-%d", round, i))))
			}
			acked += countAcked(t, waits)
			<-evicted
		}
		docs := timeline(t, rg.msgs.Messages, roomA)
		if len(docs) != acked || docs[len(docs)-1].Seq != uint64(acked) {
			t.Fatalf("stored %d messages ending at seq %d, want the %d acked ones gapless", len(docs), docs[len(docs)-1].Seq, acked)
		}
	})
}

func TestRetireRacingCloseAnswersEveryCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		ctx := t.Context()
		waits := []<-chan sendResult{sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1")), sendAsync(ctx, rg.Router, cmd(roomB, "bob", "c2"))}
		synctest.Wait()
		waits = append(waits, sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c3")), sendAsync(ctx, rg.Router, cmd(roomB, "bob", "c4")))
		synctest.Wait()
		evicted := evictAsync(ctx, rg.Router, roomA, roomB)
		closed := make(chan error, 1)
		go func() { closed <- rg.Close(context.Background()) }()
		rg.sub.open()
		if n := countAcked(t, waits); n < 2 {
			t.Fatalf("%d commands acked, want at least the 2 in flight", n)
		}
		<-evicted
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
		if err := rg.wait(); err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("%d actors left", n)
		}
	})
}

func TestRetireRacingHardStopAnswersEveryCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		ctx := t.Context()
		waits := []<-chan sendResult{sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1"))}
		synctest.Wait()
		waits = append(waits, sendAsync(ctx, rg.Router, cmd(roomA, "bob", "c2")))
		synctest.Wait()
		evicted := evictAsync(ctx, rg.Router, roomA)
		rg.cancel()
		for _, w := range waits {
			expectErr(t, (<-w).err, domain.ErrRetryLater)
		}
		<-evicted
		expectErr(t, rg.wait(), context.Canceled)
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("%d actors left after hard stop", n)
		}
		if _, _, aborts := rg.cids.calls(); len(aborts) != 0 {
			t.Fatalf("hard stop during retire released in-flight reservations: %v", aborts)
		}
	})
}

func countAcked(t *testing.T, waits []<-chan sendResult) int {
	t.Helper()
	acked := 0
	for i, w := range waits {
		switch got := <-w; {
		case got.err == nil:
			acked++
		case !errors.Is(got.err, domain.ErrRetryLater):
			t.Fatalf("command %d: %v, want an ack or ErrRetryLater", i, got.err)
		}
	}
	return acked
}

func roomSharingSlotWith(room uint64) uint64 {
	other := room + 1
	for slotmap.Of(other) != slotmap.Of(room) {
		other++
	}
	return other
}

func slotsOf(rooms ...uint64) []uint16 {
	out := make([]uint16, len(rooms))
	for i, room := range rooms {
		out[i] = slotmap.Of(room)
	}
	return out
}

func evictAsync(ctx context.Context, r *actor.Router, rooms ...uint64) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.EvictSlots(ctx, slotsOf(rooms...))
	}()
	return done
}
