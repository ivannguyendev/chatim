package slot

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestOwnsExpiresBeforeOtherCoresMayTakeOver(t *testing.T) {
	_, rdb := newRedis(t)
	a := newManager(t, rdb, "core-a", nil)
	t0 := time.Unix(1_700_000_000, 0)
	a.now = func() time.Time { return t0 }
	stepAll(t, a)
	cutoff := t0.Add(a.cfg.HeartbeatTTL - a.cfg.Tick)
	a.now = func() time.Time { return cutoff.Add(-100 * time.Millisecond) }
	if !a.Owns(0) {
		t.Fatal("Owns(0) = false while no other core may take the slot over yet")
	}
	a.now = func() time.Time { return cutoff.Add(100 * time.Millisecond) }
	if a.Owns(0) {
		t.Fatal("Owns(0) = true after other cores may already have taken the slot over")
	}
}

func TestOwnershipStampedAtStepStart(t *testing.T) {
	_, rdb := newRedis(t)
	a := newManager(t, rdb, "core-a", nil)
	t0 := time.Unix(1_700_000_000, 0)
	clock := t0
	a.now = func() time.Time { return clock }
	rdb.AddHook(slowRoundTrips{clock: &clock, delay: 2 * time.Second})
	stepAll(t, a)
	clock = t0.Add(a.cfg.HeartbeatTTL - a.cfg.Tick + 100*time.Millisecond)
	if a.Owns(0) {
		t.Fatal("Owns(0) = true past the heartbeat window measured from step start")
	}
}

type slowRoundTrips struct {
	clock *time.Time
	delay time.Duration
}

func (h slowRoundTrips) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h slowRoundTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		*h.clock = h.clock.Add(h.delay)
		return next(ctx, cmd)
	}
}

func (h slowRoundTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		*h.clock = h.clock.Add(h.delay)
		return next(ctx, cmds)
	}
}

func TestReleaseDropsLowestScoreSlots(t *testing.T) {
	_, rdb := newRedis(t)
	a, b := newManager(t, rdb, "core-a", nil), newManager(t, rdb, "core-b", nil)
	stepAll(t, a)
	stepAll(t, b)
	if n := len(b.Owned()); n != 0 {
		t.Fatalf("core-b claimed %d slots held by a live core", n)
	}
	stepAll(t, a)
	kept := a.Owned()
	if len(kept) != slotmap.Count/2 {
		t.Fatalf("core-a owns %d slots, want %d", len(kept), slotmap.Count/2)
	}
	var keptScores, releasedScores []uint64
	for s := range uint16(slotmap.Count) {
		score := slotmap.Score(s, "core-a")
		if slices.Contains(kept, s) {
			keptScores = append(keptScores, score)
		} else {
			releasedScores = append(releasedScores, score)
		}
	}
	if lo, hi := slices.Min(keptScores), slices.Max(releasedScores); lo <= hi {
		t.Fatalf("lowest kept score %d <= highest released score %d", lo, hi)
	}
}
