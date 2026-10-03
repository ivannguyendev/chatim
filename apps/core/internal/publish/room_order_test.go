package publish_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
)

func TestARoomsNextBatchWaitsForItsEarlierBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, fastSetup)
		rg.js.Hold()
		rg.enqueue(t, roomA, 1, 2)
		rg.enqueue(t, roomA, 3)
		synctest.Wait()
		if n := rg.js.Held(); n != 2 {
			t.Fatalf("%d publishes in flight, want only the 2 of the first batch", n)
		}
		rg.js.Release()
		synctest.Wait()
		if got, want := storedIDs(rg.js), []string{"101-0-1", "101-0-2", "101-0-3"}; !slices.Equal(got, want) {
			t.Fatalf("stored %v, want %v", got, want)
		}
	})
}

func TestARetriedEventStillPublishesBeforeTheRoomsNextBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, fastSetup)
		nacked := false
		rg.js.NackWhen(func(m *nats.Msg) error {
			if publishtest.MsgID(m) == "101-0-1" && !nacked {
				nacked = true
				return errNack
			}
			return nil
		})
		rg.enqueue(t, roomA, 1)
		rg.enqueue(t, roomA, 2)
		synctest.Wait()
		time.Sleep(2 * fastSetup.MaxBackoff)
		synctest.Wait()
		if got, want := storedIDs(rg.js), []string{"101-0-1", "101-0-2"}; !slices.Equal(got, want) {
			t.Fatalf("stored %v, want the retried first batch before the second", got)
		}
		if n := attemptsOf(rg.js, "101-0-1"); n != 2 {
			t.Fatalf("first event attempted %d times, want 2", n)
		}
	})
}

func TestAnAbandonedEventReleasesItsRoomAndNeverHoldsOtherRooms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := fastSetup
		cfg.Shards = 1
		rg := started(t, cfg)
		rg.js.NackWhen(nackIDs("101-0-1"))
		rg.enqueue(t, roomA, 1)
		rg.enqueue(t, roomA, 2)
		rg.enqueue(t, roomB, 1)
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if got, want := storedIDs(rg.js), []string{"202-0-1", "101-0-2"}; !slices.Equal(got, want) {
			t.Fatalf("stored %v, want room B at once and room A's second batch after the first gave up", got)
		}
		first, second := attemptIndexes(rg.js, "101-0-1"), attemptIndexes(rg.js, "101-0-2")
		if len(first) != cfg.Attempts || len(second) != 1 || second[0] < first[len(first)-1] {
			t.Fatalf("attempt order: first %v second %v, want %d tries of the first before the second", first, second, cfg.Attempts)
		}
		if n := rg.sink.Count(abandonedMsg); n != 1 {
			t.Fatalf("logged %d abandoned publishes, want 1", n)
		}
	})
}
