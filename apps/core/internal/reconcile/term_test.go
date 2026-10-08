package reconcile_test

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestIdleUntilItOwnsTheLeaderSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		rg.owner.leading.Store(false)
		rg.start(t)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(3 * tick)
		synctest.Wait()
		if got := attemptIDs(rg.js); len(got) != 0 {
			t.Fatalf("attempts while not leading = %v, want none", got)
		}
	})
}

func TestLeadershipHandoverResumesFromTheConfirmedPosition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(2 * tick)
		synctest.Wait()
		rg.owner.leading.Store(false)
		time.Sleep(2 * tick)
		synctest.Wait()
		rg.insert(t, room, 2)
		rg.owner.leading.Store(true)
		time.Sleep(3 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1, 2)) {
			t.Fatalf("stored = %v, want %s then %s", got, recordID(1), recordID(2))
		}
	})
}

func TestLostHistoryRestartsFromNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		rg.feed.LoseHistory()
		rg.start(t)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := rg.sink.Count(historyLostMsg); got != 1 {
			t.Fatalf("%q logged %d times, want 1", historyLostMsg, got)
		}
		rg.insert(t, room, 1)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1)) {
			t.Fatalf("stored = %v, want %s", got, recordID(1))
		}
	})
}

func TestBusyFeedIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		busy := &busyFeed{}
		busy.refusals.Store(2)
		rg := newRig(t, func(f store.ChangeFeed) store.ChangeFeed {
			busy.ChangeFeed = f
			return busy
		}).start(t)
		time.Sleep(3 * tick)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1)) {
			t.Fatalf("stored = %v, want %s after the feed frees up", got, recordID(1))
		}
	})
}

func TestCloseSettlesInFlightPublishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1)
		synctest.Wait()
		closed := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closed <- rg.Close(ctx)
		}()
		time.Sleep(tick / 2)
		rg.js.Release()
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v", err)
		}
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed after Close = %d, want 1", got)
		}
	})
}
