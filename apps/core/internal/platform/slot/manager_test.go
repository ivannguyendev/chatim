package slot

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestSingleCoreClaimsAllSlots(t *testing.T) {
	mr, rdb := newRedis(t)
	a := newManager(t, rdb, "core-a")
	stepAll(t, a)
	if n := len(a.Owned()); n != slotmap.Count {
		t.Fatalf("core-a owns %d slots, want %d", n, slotmap.Count)
	}
	assertPartition(t, mr, a)
	if !a.Owns(0) {
		t.Fatal("Owns(0) = false for a freshly claimed slot")
	}
}

func TestCoresConvergeToFairShares(t *testing.T) {
	mr, rdb := newRedis(t)
	ms := []*Manager{newManager(t, rdb, "core-a"), newManager(t, rdb, "core-b"), newManager(t, rdb, "core-c")}
	for range 4 {
		stepAll(t, ms...)
	}
	assertPartition(t, mr, ms...)
	assertShares(t, 340, 342, ms...)
}

func TestDeadCoreSlotsAreTakenOver(t *testing.T) {
	mr, rdb := newRedis(t)
	a, b, c := newManager(t, rdb, "core-a"), newManager(t, rdb, "core-b"), newManager(t, rdb, "core-c")
	for range 4 {
		stepAll(t, a, b, c)
	}

	for range 10 {
		advance(t, mr, rdb, time.Second)
		stepAll(t, a, b)
	}
	if mr.Exists(slotmap.CoreKey("core-c")) {
		t.Fatal("core-c heartbeat should have expired")
	}
	assertPartition(t, mr, a, b)
	assertShares(t, 512, 512, a, b)
}

func TestRedisDataLossReconverges(t *testing.T) {
	mr, rdb := newRedis(t)
	ms := []*Manager{newManager(t, rdb, "core-a"), newManager(t, rdb, "core-b"), newManager(t, rdb, "core-c")}
	for range 4 {
		stepAll(t, ms...)
	}
	mr.FlushAll()
	for range 5 {
		stepAll(t, ms...)
		assertDisjoint(t, ms...)
	}
	assertPartition(t, mr, ms...)
	assertShares(t, 340, 342, ms...)
}

func TestReleaseAllHandsSlotsBack(t *testing.T) {
	mr, rdb := newRedis(t)
	drained := &hookLog{}
	a := newManager(t, rdb, "core-a", withBeforeRelease(drained.record))
	b := newManager(t, rdb, "core-b")
	for range 3 {
		stepAll(t, a, b)
	}
	held := a.Owned()
	drained.reset()
	if err := a.ReleaseAll(context.Background()); err != nil {
		t.Fatalf("ReleaseAll: %v", err)
	}
	if batches := drained.batches(); len(batches) != 1 || !sameSlots(batches[0], held) || len(a.Owned()) != 0 {
		t.Fatalf("drained batches %v for %d held slots, still owns %d; want one batch of every held slot", batchSizes(batches), len(held), len(a.Owned()))
	}
	for _, s := range held {
		if mr.Exists(slotmap.SlotKey(s)) {
			t.Fatalf("slot %d lease still in redis after ReleaseAll", s)
		}
	}
	if mr.Exists(slotmap.CoreKey("core-a")) || registered(mr, "core-a") {
		t.Fatal("ReleaseAll must remove the heartbeat and the registry member")
	}
	stepAll(t, b)
	assertPartition(t, mr, b)
}

func TestLostLeaseStopsOwnership(t *testing.T) {
	mr, rdb := newRedis(t)
	a := newManager(t, rdb, "core-a")
	stepAll(t, a)
	mr.FlushAll()
	b := newManager(t, rdb, "core-b")
	stepAll(t, b)
	stepAll(t, a)
	if a.Owns(0) || len(a.Owned()) != 0 {
		t.Fatalf("core-a still owns %d slots after losing its leases", len(a.Owned()))
	}
}

func TestNewValidatesConfig(t *testing.T) {
	_, rdb := newRedis(t)
	bad := []Config{
		{CoreID: ""},
		{CoreID: "core*"},
		{CoreID: "core-a", Tick: time.Second, LeaseTTL: 2 * time.Second},
		{CoreID: "core-a", Tick: time.Second, HeartbeatTTL: 2 * time.Second, LeaseTTL: 10 * time.Second},
		{CoreID: "core-a", Tick: time.Second, HookTimeout: time.Second},
		{CoreID: "core-a", Tick: time.Second, HookTimeout: -time.Millisecond},
	}
	for _, cfg := range bad {
		if _, err := New(rdb, cfg, nil); err == nil {
			t.Errorf("New(%+v) succeeded, want error", cfg)
		}
	}
}

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.SetTime(redisEpoch)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func newManager(t *testing.T, rdb *redis.Client, id string, tune ...func(*Config)) *Manager {
	t.Helper()
	cfg := Config{CoreID: id, Addr: id + ":9000"}
	for _, f := range tune {
		f(&cfg)
	}
	m, err := New(rdb, cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New(%s): %v", id, err)
	}
	return m
}

func stepAll(t *testing.T, ms ...*Manager) {
	t.Helper()
	for _, m := range ms {
		if err := m.Step(context.Background()); err != nil {
			t.Fatalf("%s Step: %v", m.cfg.CoreID, err)
		}
	}
}

func assertPartition(t *testing.T, mr *miniredis.Miniredis, ms ...*Manager) {
	t.Helper()
	owner := assertDisjoint(t, ms...)
	for s := range uint16(slotmap.Count) {
		inRedis, _ := mr.Get(slotmap.SlotKey(s))
		if owner[s] == "" || inRedis != owner[s] {
			t.Fatalf("slot %d: local owner %q, redis owner %q", s, owner[s], inRedis)
		}
	}
}

func assertDisjoint(t *testing.T, ms ...*Manager) map[uint16]string {
	t.Helper()
	owner := map[uint16]string{}
	for _, m := range ms {
		for _, s := range m.Owned() {
			if prev, dup := owner[s]; dup {
				t.Fatalf("slot %d owned by both %s and %s", s, prev, m.cfg.CoreID)
			}
			owner[s] = m.cfg.CoreID
		}
	}
	return owner
}

func assertShares(t *testing.T, lo, hi int, ms ...*Manager) {
	t.Helper()
	for _, m := range ms {
		if n := len(m.Owned()); n < lo || n > hi {
			t.Fatalf("%s owns %d slots, want %d..%d", m.cfg.CoreID, n, lo, hi)
		}
	}
}
