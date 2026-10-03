package publish_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
)

func TestIdleRoomIsForgottenAndRebasedFromRedis(t *testing.T) {
	cfg := fastSetup
	cfg.RoomIdle = 5 * time.Millisecond
	rg := started(t, cfg)
	rg.enqueue(t, roomA, 1, 2)
	waitWatermark(t, rg.mr, roomA, 2)
	time.Sleep(30 * time.Millisecond)

	mustSet(t, rg.mr, publish.WatermarkKey(roomA), "5")
	rg.enqueue(t, roomA, 6)
	waitWatermark(t, rg.mr, roomA, 6)
}

func TestBusyRoomKeepsItsTrackingWhileIdleRoomsAreSwept(t *testing.T) {
	cfg := fastSetup
	cfg.RoomIdle = 5 * time.Millisecond
	rg := started(t, cfg)
	rg.js.NackWhen(nackIDs("101-1"))
	rg.enqueue(t, roomA, 1, 2, 3)
	waitWatermark(t, rg.mr, roomA, 0)
	time.Sleep(30 * time.Millisecond)
	rg.js.NackWhen(nil)
	rg.enqueue(t, roomA, 1)
	waitWatermark(t, rg.mr, roomA, 3)
}

func TestRoomWithTooManyEventsAboveAGapStopsTracking(t *testing.T) {
	cfg := fastSetup
	cfg.MaxAhead, cfg.Attempts, cfg.RoomIdle = 3, 1, 5*time.Millisecond
	rg := started(t, cfg)
	rg.js.NackWhen(nackIDs("101-1"))
	rg.enqueue(t, roomA, span(1, 6)...)
	eventually(t, "overflow logged", func() bool { return rg.sink.Count(overflowMsg) == 1 })
	waitWatermark(t, rg.mr, roomA, 0)
	holdsWatermark(t, rg.mr, roomA, 0)

	rg.js.NackWhen(nil)
	for p := uint64(1); p <= 6; p++ {
		rg.enqueue(t, roomA, p)
		waitWatermark(t, rg.mr, roomA, p)
	}
}

func TestTrackedRoomLimitLeavesNewRoomsUntracked(t *testing.T) {
	cfg := fastSetup
	cfg.Shards, cfg.MaxRooms = 1, 1
	rg := started(t, cfg)
	rg.js.Hold()
	rg.enqueue(t, roomA, 1)
	rg.enqueue(t, roomB, 1)
	eventually(t, "both publishes in flight", func() bool { return rg.js.Held() == 2 })
	if n := rg.sink.Count(overflowMsg); n != 1 {
		t.Fatalf("logged tracking overflow %d times, want 1", n)
	}
	rg.js.Release()
	waitWatermark(t, rg.mr, roomA, 1)
	time.Sleep(30 * time.Millisecond)
	if got, ok := watermark(rg.mr, roomB); ok {
		t.Fatalf("untracked room got watermark %d", got)
	}

	rg.enqueue(t, roomB, 2)
	waitWatermark(t, rg.mr, roomB, 2)
}
