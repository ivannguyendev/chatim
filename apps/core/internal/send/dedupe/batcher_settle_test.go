package dedupe

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestCommitAndAbortDoNotWaitForRedis(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, reg, _ := startFake(t, testBatch, true)
		rooms := roomsOnShard(testBatch.Shards, 1, 3)
		a, c := roomEntries(rooms[0], 1), roomEntries(rooms[1], 2)
		d, e := roomKeys(rooms[2], 2), roomKeys(rooms[0], 1)
		expectNil(t, b.Commit(t.Context(), a))
		synctest.Wait()
		expectNil(t, b.Commit(t.Context(), c))
		expectNil(t, b.Abort(t.Context(), d))
		expectNil(t, b.Abort(t.Context(), e))
		expectNil(t, b.Commit(t.Context(), nil))
		expectNil(t, b.Abort(t.Context(), nil))
		synctest.Wait()
		reg.open()
		synctest.Wait()
		_, commits, aborts := reg.calls()
		if want := [][]Entry{a, c}; !reflect.DeepEqual(commits, want) {
			t.Fatalf("commit rounds = %v, want %v", commits, want)
		}
		if want := [][]Key{slices.Concat(d, e)}; !reflect.DeepEqual(aborts, want) {
			t.Fatalf("abort rounds = %v, want %v", aborts, want)
		}
	})
}

func TestCloseFlushesQueuedCommits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, reg, _ := startFake(t, testBatch, true)
		rooms := roomsOnShard(testBatch.Shards, 0, 2)
		a, c := roomEntries(rooms[0], 1), roomEntries(rooms[1], 3)
		expectNil(t, b.Commit(t.Context(), a))
		synctest.Wait()
		expectNil(t, b.Commit(t.Context(), c))
		closed := closeAsync(context.Background(), b)
		synctest.Wait()
		reg.open()
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v", err)
		}
		if _, commits, _ := reg.calls(); !reflect.DeepEqual(commits, [][]Entry{a, c}) {
			t.Fatalf("commit rounds after Close = %v", commits)
		}
	})
}

func TestCloseAnswersQueuedReservesAndRejectsNewCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, reg, _ := startFake(t, testBatch, true)
		rooms := roomsOnShard(testBatch.Shards, 1, 2)
		inFlight := reserveAsync(t.Context(), b, roomKeys(rooms[0], 1))
		synctest.Wait()
		queued := reserveAsync(t.Context(), b, roomKeys(rooms[1], 1))
		synctest.Wait()
		closed := closeAsync(context.Background(), b)
		synctest.Wait()
		reg.open()
		expectVerdicts(t, <-inFlight, roomKeys(rooms[0], 1))
		if got := <-queued; !errors.Is(got.err, ErrBatcherClosed) {
			t.Fatalf("queued Reserve at Close = %v, %v, want ErrBatcherClosed", got.verdicts, got.err)
		}
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v", err)
		}
		if _, err := b.Reserve(t.Context(), roomKeys(rooms[0], 1)); !errors.Is(err, ErrBatcherClosed) {
			t.Fatalf("Reserve after Close = %v, want ErrBatcherClosed", err)
		}
		expectNil(t, b.Commit(t.Context(), roomEntries(rooms[0], 1)))
		expectNil(t, b.Abort(t.Context(), roomKeys(rooms[0], 1)))
		if reserves, commits, aborts := reg.calls(); len(reserves) != 1 || len(commits) != 0 || len(aborts) != 0 {
			t.Fatalf("store calls after Close: reserves %v, commits %v, aborts %v", reserves, commits, aborts)
		}
	})
}

func TestCloseGivesUpWhenItsContextExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, reg, _ := startFake(t, testBatch, true)
		rooms := roomsOnShard(testBatch.Shards, 0, 2)
		a := roomEntries(rooms[0], 1)
		expectNil(t, b.Commit(t.Context(), a))
		synctest.Wait()
		expectNil(t, b.Commit(t.Context(), roomEntries(rooms[1], 1)))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		closed := closeAsync(ctx, b)
		time.Sleep(2 * time.Second)
		reg.open()
		if err := <-closed; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close past its deadline = %v, want DeadlineExceeded", err)
		}
		if _, commits, _ := reg.calls(); !reflect.DeepEqual(commits, [][]Entry{a}) {
			t.Fatalf("commit rounds after an abandoned Close = %v, want only the in-flight one", commits)
		}
	})
}

func TestFullSettleQueueDropsAndLogsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := BatchConfig{Shards: 1, MaxKeys: 256, Queue: 1}
		b, reg, sink := startFake(t, cfg, true)
		room := roomsOnShard(1, 0, 1)[0]
		a, c, e := roomEntries(room, 1), roomEntries(room, 2), roomEntries(room, 4)
		expectNil(t, b.Commit(t.Context(), a))
		synctest.Wait()
		expectNil(t, b.Commit(t.Context(), c))
		expectNil(t, b.Commit(t.Context(), roomEntries(room, 3)))
		expectNil(t, b.Abort(t.Context(), roomKeys(room, 2)))
		if n := b.Dropped(); n != 5 {
			t.Fatalf("Dropped() = %d, want 5", n)
		}
		if n := sink.Count(settleFullMsg); n != 1 {
			t.Fatalf("logged a full settle queue %d times, want 1", n)
		}
		reg.open()
		synctest.Wait()
		expectNil(t, b.Commit(t.Context(), e))
		synctest.Wait()
		if _, commits, _ := reg.calls(); !reflect.DeepEqual(commits, [][]Entry{a, c, e}) {
			t.Fatalf("commit rounds = %v, want the queued ones only", commits)
		}
		if n := b.Dropped(); n != 5 {
			t.Fatalf("Dropped() after the queue drained = %d, want 5", n)
		}
	})
}

func expectNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
