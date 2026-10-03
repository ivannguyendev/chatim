package recovery_test

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/recovery"
	"github.com/ivannguyendev/chatim/apps/core/internal/slot"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestMain(m *testing.M) {
	testlog.SilenceRedis()
	goleak.VerifyTestMain(m)
}

const (
	tenant        = "acme"
	roomA  uint64 = 101
)

var (
	_ recovery.Slots    = (*slot.Manager)(nil)
	_ recovery.Rooms    = (*actor.Router)(nil)
	_ recovery.Timeline = store.Messages(nil)
)

var (
	quiet     = slog.New(slog.DiscardHandler)
	slotA     = slotmap.Of(roomA)
	passSetup = recovery.Config{Interval: time.Hour, GroupDeadline: time.Second, RedisTimeout: time.Second}
)

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), ContextTimeoutEnabled: true, MaxRetries: -1, DialerRetries: 1})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func roomsInSlot(slot uint16, n int) []uint64 {
	var out []uint64
	for id := uint64(1); len(out) < n; id++ {
		if slotmap.Of(id) == slot {
			out = append(out, id)
		}
	}
	return out
}

func seed(t *testing.T, m store.Messages, room uint64, at time.Time, pts ...uint64) {
	t.Helper()
	msgs := make([]domain.Message, len(pts))
	for i, p := range pts {
		msgs[i] = domain.Message{Room: room, Seq: p, Pts: p, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "seeded", CID: fmt.Sprintf("s%d", p), CreatedAt: at.UTC().Truncate(time.Millisecond)}
	}
	for i, r := range m.Insert(context.Background(), msgs) {
		if r.Outcome != store.Inserted {
			t.Fatalf("seed room %d pts %d: %v", room, pts[i], r.Outcome)
		}
	}
}

func mark(t *testing.T, mr *miniredis.Miniredis, room uint64, at time.Time) {
	t.Helper()
	if _, err := mr.ZAdd(publish.ActiveKey(slotmap.Of(room)), float64(at.UnixMilli()), pbconv.RoomID(room)); err != nil {
		t.Fatalf("mark room %d: %v", room, err)
	}
}

func setWatermark(t *testing.T, mr *miniredis.Miniredis, room uint64, v string) {
	t.Helper()
	if err := mr.Set(publish.WatermarkKey(room), v); err != nil {
		t.Fatalf("set watermark of room %d: %v", room, err)
	}
}

func isActive(mr *miniredis.Miniredis, room uint64) bool {
	members, err := mr.ZMembers(publish.ActiveKey(slotmap.Of(room)))
	return err == nil && slices.Contains(members, pbconv.RoomID(room))
}

type clock struct {
	mu    sync.Mutex
	base  time.Time
	shift time.Duration
}

func fixedClock() *clock { return &clock{base: time.Now().Truncate(time.Millisecond)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.base.IsZero() {
		return time.Now().Add(c.shift)
	}
	return c.base.Add(c.shift)
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shift += d
}

type fakeSlots struct {
	mu    sync.Mutex
	owned []uint16
	lost  map[uint16]bool
}

func owning(slots ...uint16) *fakeSlots { return &fakeSlots{owned: slots, lost: map[uint16]bool{}} }

func (f *fakeSlots) Owned() []uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.owned)
}

func (f *fakeSlots) Owns(slot uint16) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.owned, slot) && !f.lost[slot]
}

func (f *fakeSlots) lose(slot uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lost[slot] = true
}

func newSweeper(t *testing.T, deps recovery.Deps, cfg recovery.Config, clk *clock) *recovery.Sweeper {
	t.Helper()
	sw, err := recovery.New(deps, cfg, quiet)
	if err != nil {
		t.Fatalf("recovery.New: %v", err)
	}
	if clk != nil {
		sw.SetClock(clk.now)
	}
	return sw
}

func runSweeper(t *testing.T, sw *recovery.Sweeper) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sw.Run(ctx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		<-done
	})
	t.Cleanup(stop)
	return stop
}

func awaitSignal[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(30 * time.Second):
		t.Fatalf("%s did not happen within 30s", what)
		panic("unreachable")
	}
}
