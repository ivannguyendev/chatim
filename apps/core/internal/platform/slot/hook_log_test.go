package slot

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	crowdedForRelease = 679
	crowdedForLoss    = 1014
)

type hookCall struct {
	tag      string
	slots    []uint16
	deadline time.Time
	ctxErr   error
}

type hookLog struct {
	block  bool
	during func(ctx context.Context, tag string, slots []uint16)

	mu    sync.Mutex
	calls []hookCall
}

func (h *hookLog) record(ctx context.Context, slots []uint16) { h.note(ctx, "", slots) }

func (h *hookLog) tagged(tag string) func(context.Context, []uint16) {
	return func(ctx context.Context, slots []uint16) { h.note(ctx, tag, slots) }
}

func (h *hookLog) note(ctx context.Context, tag string, slots []uint16) {
	if h.during != nil {
		h.during(ctx, tag, slots)
	}
	if h.block {
		<-ctx.Done()
	}
	deadline, _ := ctx.Deadline()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, hookCall{tag: tag, slots: slices.Clone(slots), deadline: deadline, ctxErr: ctx.Err()})
}

func (h *hookLog) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = nil
}

func (h *hookLog) batches() [][]uint16 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([][]uint16, len(h.calls))
	for i, c := range h.calls {
		out[i] = c.slots
	}
	return out
}

func (h *hookLog) tags() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.calls))
	for i, c := range h.calls {
		out[i] = c.tag
	}
	return out
}

func (h *hookLog) all() []hookCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

func withBeforeRelease(hook func(context.Context, []uint16)) func(*Config) {
	return func(c *Config) { c.BeforeRelease = hook }
}

func withAfterClaim(hook func(context.Context, []uint16)) func(*Config) {
	return func(c *Config) { c.AfterClaim = hook }
}

func withAfterLose(hook func(context.Context, []uint16)) func(*Config) {
	return func(c *Config) { c.AfterLose = hook }
}

func sameSlots(a, b []uint16) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

func distinct(slots []uint16) bool {
	sorted := slices.Clone(slots)
	slices.Sort(sorted)
	return len(slices.Compact(sorted)) == len(slots)
}

func batchSizes(batches [][]uint16) []int {
	out := make([]int, len(batches))
	for i, b := range batches {
		out[i] = len(b)
	}
	return out
}

func claimLeftovers(t *testing.T, mr *miniredis.Miniredis, m *Manager, crowded uint16) {
	t.Helper()
	heartbeat(t, mr, "core-x")
	overwriteLeases(t, mr, "core-x", slotRange(0, crowded)...)
	stepAll(t, m)
}

func heartbeat(t *testing.T, mr *miniredis.Miniredis, core string) {
	t.Helper()
	if err := mr.Set(slotmap.CoreKey(core), core+":9000"); err != nil {
		t.Fatalf("heartbeat %s: %v", core, err)
	}
	if _, err := mr.ZAdd(slotmap.CoreRegistryKey, slotmap.CoreExpiryScore(redisEpoch.Add(time.Hour)), core); err != nil {
		t.Fatalf("register %s: %v", core, err)
	}
}

func overwriteLeases(t *testing.T, mr *miniredis.Miniredis, owner string, slots ...uint16) {
	t.Helper()
	for _, s := range slots {
		if err := mr.Set(slotmap.SlotKey(s), owner); err != nil {
			t.Fatalf("overwrite lease %d: %v", s, err)
		}
	}
}

func assertLeasesGone(t *testing.T, mr *miniredis.Miniredis, slots []uint16) {
	t.Helper()
	for _, s := range slots {
		if mr.Exists(slotmap.SlotKey(s)) {
			t.Fatalf("slot %d lease still in redis after release", s)
		}
	}
}

func nearStepStart(begin, deadline time.Time, timeout time.Duration) bool {
	return deadline.Sub(begin) <= timeout+100*time.Millisecond
}

func slotRange(from, to uint16) []uint16 {
	out := make([]uint16, 0, to-from)
	for s := from; s < to; s++ {
		out = append(out, s)
	}
	return out
}

func without(all, drop []uint16) []uint16 {
	return slices.DeleteFunc(slices.Clone(all), func(s uint16) bool { return slices.Contains(drop, s) })
}
