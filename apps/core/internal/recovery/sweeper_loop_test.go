package recovery_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/recovery"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

type loopWorld struct {
	slots []uint16
	rooms []uint64
	owner *fakeSlots
	calls *recorder
	sw    *recovery.Sweeper
}

func newLoopWorld(t *testing.T, cfg recovery.Config, n int) *loopWorld {
	t.Helper()
	mr, rdb := newRedis(t)
	msgs := memstore.NewMessages()
	lw := &loopWorld{calls: newRecorder(nil)}
	for i := range n {
		slot := (slotA + uint16(i)) % slotmap.Count
		room := roomsInSlot(slot, 1)[0]
		seed(t, msgs, room, time.Now(), 1)
		mark(t, mr, room, time.Now())
		lw.slots, lw.rooms = append(lw.slots, slot), append(lw.rooms, room)
	}
	lw.owner = owning(lw.slots...)
	lw.sw = newSweeper(t, recovery.Deps{Slots: lw.owner, Rooms: lw.calls, Msgs: msgs, Redis: rdb}, cfg, nil)
	return lw
}

func (lw *loopWorld) recoveredRooms() []uint64 {
	var out []uint64
	for _, c := range lw.calls.list() {
		out = append(out, c.room)
	}
	return out
}

func TestTriggerNeverBlocksAndCoalescesPendingSlots(t *testing.T) {
	lw := newLoopWorld(t, passSetup, 2)
	for range 1000 {
		lw.sw.Trigger([]uint16{lw.slots[0]})
	}
	lw.sw.Trigger([]uint16{slotmap.Count, 5000})
	runSweeper(t, lw.sw)
	lw.calls.awaitDone(t, lw.rooms[0])
	lw.sw.Trigger(lw.slots[1:])
	lw.calls.awaitDone(t, lw.rooms[1])
	if got := lw.recoveredRooms(); !slices.Equal(got, lw.rooms) {
		t.Fatalf("recovered %v, want each triggered slot once: %v", got, lw.rooms)
	}
}

func TestRunStopsOnContextCancelEvenMidPass(t *testing.T) {
	lw := newLoopWorld(t, passSetup, 1)
	lw.calls.hold(lw.rooms[0])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- lw.sw.Run(ctx) }()
	lw.sw.Trigger(lw.slots)
	awaitSignal(t, "recovery started", lw.calls.entered)
	cancel()
	if err := awaitSignal(t, "Run returned", done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want %v", err, context.Canceled)
	}
	if err := lw.sw.Run(context.Background()); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("second Run = %v, want an already-started error", err)
	}
}

func TestPeriodicPassSkipsSlotsNoLongerOwned(t *testing.T) {
	cfg := passSetup
	cfg.Interval = 20 * time.Millisecond
	lw := newLoopWorld(t, cfg, 2)
	lost, kept := lw.slots[0], lw.slots[1]
	lw.owner.lose(lost)
	lw.sw.Trigger([]uint16{lost})
	runSweeper(t, lw.sw)
	lw.calls.awaitDone(t, lw.rooms[1])
	if slices.Contains(lw.recoveredRooms(), lw.rooms[0]) {
		t.Fatalf("recovered room %d of slot %d after losing it", lw.rooms[0], lost)
	}
	if !lw.owner.Owns(kept) {
		t.Fatal("kept slot reported lost")
	}
}

func TestTriggeredSlotsGoBeforeTheRestOfAPeriodicPass(t *testing.T) {
	cfg := passSetup
	cfg.Interval = 20 * time.Millisecond
	lw := newLoopWorld(t, cfg, 4)
	gate := lw.calls.hold(lw.rooms[0])
	runSweeper(t, lw.sw)
	awaitSignal(t, "first slot of the periodic pass", lw.calls.entered)
	lw.sw.Trigger(lw.slots[3:])
	close(gate)
	lw.calls.awaitDone(t, lw.rooms[2])
	want := []uint64{lw.rooms[0], lw.rooms[3], lw.rooms[1], lw.rooms[2]}
	if got := lw.recoveredRooms(); len(got) < 4 || !slices.Equal(got[:4], want) {
		t.Fatalf("recovery order %v, want the triggered room right after the blocked one: %v", got, want)
	}
}

func TestNewRejectsMissingDependenciesAndUnsafeConfig(t *testing.T) {
	_, rdb := newRedis(t)
	deps := recovery.Deps{Slots: owning(), Rooms: newRecorder(nil), Msgs: memstore.NewMessages(), Redis: rdb}
	minimal := recovery.Config{GroupDeadline: 3 * time.Second, RemoveAfter: 9 * time.Second}
	if _, err := recovery.New(deps, minimal, quiet); err != nil {
		t.Fatalf("RemoveAfter at exactly mark interval + group deadline + skew rejected: %v", err)
	}
	if _, err := recovery.New(deps, recovery.Config{GroupDeadline: time.Second}, quiet); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
	withDeps := func(f func(*recovery.Deps)) recovery.Deps { d := deps; f(&d); return d }
	withConfig := func(f func(*recovery.Config)) recovery.Config { c := minimal; f(&c); return c }
	cases := map[string]struct {
		deps recovery.Deps
		cfg  recovery.Config
	}{
		"no slots":             {withDeps(func(d *recovery.Deps) { d.Slots = nil }), minimal},
		"no rooms":             {withDeps(func(d *recovery.Deps) { d.Rooms = nil }), minimal},
		"no timeline":          {withDeps(func(d *recovery.Deps) { d.Msgs = nil }), minimal},
		"no redis":             {withDeps(func(d *recovery.Deps) { d.Redis = nil }), minimal},
		"no group deadline":    {deps, withConfig(func(c *recovery.Config) { c.GroupDeadline = 0 })},
		"remove too early":     {deps, withConfig(func(c *recovery.Config) { c.RemoveAfter -= time.Millisecond })},
		"room timeout too low": {deps, withConfig(func(c *recovery.Config) { c.RoomTimeout = c.GroupDeadline })},
		"negative workers":     {deps, withConfig(func(c *recovery.Config) { c.Workers = -1 })},
		"negative interval":    {deps, withConfig(func(c *recovery.Config) { c.Interval = -time.Second })},
		"huge batch":           {deps, withConfig(func(c *recovery.Config) { c.Batch = 1_000_000 })},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := recovery.New(tc.deps, tc.cfg, quiet); err == nil {
				t.Fatal("New accepted it")
			}
		})
	}
}
