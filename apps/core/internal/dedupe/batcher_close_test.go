package dedupe

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestCloseIsNotBlockedByAFullReserveQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := BatchConfig{Shards: 1, MaxKeys: 256, Queue: 1}
		b, reg, _ := startFake(t, cfg, true)
		rooms := roomsOnShard(1, 0, 3)
		inFlight := reserveAsync(t.Context(), b, roomKeys(rooms[0], 1))
		synctest.Wait()
		queued := reserveAsync(context.Background(), b, roomKeys(rooms[1], 1))
		synctest.Wait()
		blocked := reserveAsync(context.Background(), b, roomKeys(rooms[2], 1))
		synctest.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		closed := closeAsync(ctx, b)
		synctest.Wait()
		if got := <-blocked; !errors.Is(got.err, ErrBatcherClosed) {
			t.Fatalf("Reserve blocked on a full queue at Close = %v, %v, want ErrBatcherClosed", got.verdicts, got.err)
		}
		expectNil(t, b.Commit(t.Context(), roomEntries(rooms[0], 1)))
		expectNil(t, b.Abort(t.Context(), roomKeys(rooms[0], 1)))
		reg.open()
		expectVerdicts(t, <-inFlight, roomKeys(rooms[0], 1))
		if got := <-queued; !errors.Is(got.err, ErrBatcherClosed) {
			t.Fatalf("queued Reserve at Close = %v, %v, want ErrBatcherClosed", got.verdicts, got.err)
		}
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v, want nil within its deadline", err)
		}
	})
}

func TestCloseBeforeRunAnswersQueuedCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &fakeRegistry{gate: make(chan struct{})}
		b, err := NewBatcher(reg, testBatch, nil)
		if err != nil {
			t.Fatalf("NewBatcher: %v", err)
		}
		room := roomsOnShard(testBatch.Shards, 0, 1)[0]
		queued := reserveAsync(context.Background(), b, roomKeys(room, 1))
		expectNil(t, b.Commit(t.Context(), roomEntries(room, 2)))
		expectNil(t, b.Abort(t.Context(), roomKeys(room, 1)))
		synctest.Wait()
		expectNil(t, b.Close(t.Context()))
		if got := <-queued; !errors.Is(got.err, ErrBatcherClosed) {
			t.Fatalf("Reserve queued before Run = %v, %v, want ErrBatcherClosed", got.verdicts, got.err)
		}
		if n := b.Dropped(); n != 3 {
			t.Fatalf("Dropped() = %d, want 3", n)
		}
		if reserves, commits, aborts := reg.calls(); len(reserves)+len(commits)+len(aborts) != 0 {
			t.Fatalf("store called without Run: %v %v %v", reserves, commits, aborts)
		}
	})
}
