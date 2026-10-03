package publish_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
)

func closeRig(t *testing.T, rg *rig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rg.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := rg.wait(); err != nil {
		t.Fatalf("Run after Close: %v", err)
	}
}

func finalWatermark(t *testing.T, mr *miniredis.Miniredis, room, want uint64) {
	t.Helper()
	if got, ok := watermark(mr, room); !ok || got != want {
		t.Fatalf("watermark of room %d after drain = %d (present %v), want %d", room, got, ok, want)
	}
}

func skip(t *testing.T, rg *rig, room uint64, pts ...uint64) {
	t.Helper()
	if err := rg.Skip(room, pts); err != nil {
		t.Fatalf("Skip(%d, %v): %v", room, pts, err)
	}
}

func TestSkippedPtsCountTowardTheWatermarkWithoutBeingPublished(t *testing.T) {
	rg := started(t, fastSetup)
	rg.enqueue(t, roomA, 1, 2)
	skip(t, rg, roomA, 3, 4)
	rg.enqueue(t, roomA, 5)
	closeRig(t, rg)

	finalWatermark(t, rg.mr, roomA, 5)
	if got, want := storedIDs(rg.js), []string{"101-1", "101-2", "101-5"}; !slices.Equal(got, want) {
		t.Fatalf("stored %v, want %v", got, want)
	}
	if n := len(rg.js.Attempts()); n != 3 {
		t.Fatalf("publish attempts = %d, want 3", n)
	}
}

func TestSkipHandedFirstPinsTheBaseBelowTheSkippedPts(t *testing.T) {
	rg := started(t, fastSetup)
	skip(t, rg, roomA, 1)
	rg.enqueue(t, roomA, 2, 3)
	closeRig(t, rg)

	finalWatermark(t, rg.mr, roomA, 3)
	if got, want := storedIDs(rg.js), []string{"101-2", "101-3"}; !slices.Equal(got, want) {
		t.Fatalf("stored %v, want %v", got, want)
	}
}

func TestSkipDoesNotCoverAPtsWhosePublishFailed(t *testing.T) {
	cfg := fastSetup
	cfg.Attempts = 1
	rg := started(t, cfg)
	rg.js.NackWhen(nackIDs("101-2"))
	rg.enqueue(t, roomA, 1, 2)
	skip(t, rg, roomA, 3)
	rg.enqueue(t, roomA, 4)
	closeRig(t, rg)

	finalWatermark(t, rg.mr, roomA, 1)
}

func TestSkipAndRepublishAtOrBelowTheWatermarkLeaveItUnchanged(t *testing.T) {
	mr := miniredis.RunT(t)
	mustSet(t, mr, publish.WatermarkKey(roomA), "5")
	rg := newRig(t, fastSetup, mr).start(t)
	skip(t, rg, roomA, 3)
	rg.enqueue(t, roomA, 4)
	rg.enqueue(t, roomA, 6)
	closeRig(t, rg)

	finalWatermark(t, mr, roomA, 6)
}

func TestSkipWithoutPtsIsANoOpAndAfterCloseIsRefused(t *testing.T) {
	rg := started(t, fastSetup)
	if err := rg.Skip(roomA, nil); err != nil {
		t.Fatalf("Skip(nil) = %v, want nil", err)
	}
	closeRig(t, rg)
	if err := rg.Skip(roomA, []uint64{1}); !errors.Is(err, publish.ErrClosed) {
		t.Fatalf("Skip after Close = %v, want %v", err, publish.ErrClosed)
	}
	if _, ok := watermark(rg.mr, roomA); ok {
		t.Fatal("watermark written for a room that was only skipped after close")
	}
}

func TestSkipIsRefusedWhenTheShardQueueIsFull(t *testing.T) {
	cfg := fastSetup
	cfg.Shards, cfg.QueueSize = 1, 1
	rg := newRig(t, cfg, nil)
	if err := rg.Skip(roomA, []uint64{1}); err != nil {
		t.Fatalf("first Skip: %v", err)
	}
	if err := rg.Skip(roomA, []uint64{2}); !errors.Is(err, publish.ErrQueueFull) {
		t.Fatalf("second Skip = %v, want %v", err, publish.ErrQueueFull)
	}
	if n := rg.sink.Count(queueFullMsg); n != 1 {
		t.Fatalf("logged queue full %d times, want 1", n)
	}
}
