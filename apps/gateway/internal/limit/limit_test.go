package limit_test

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/limit"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type clock struct{ now time.Time }

func (c *clock) read() time.Time { return c.now }

func newUsers(t *testing.T, cfg limit.UserConfig) (*limit.Users, *clock) {
	t.Helper()
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	if cfg.Now == nil {
		cfg.Now = c.read
	}
	u, err := limit.NewUsers(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return u, c
}

func spend(t *testing.T, u *limit.Users, key limit.Key) int {
	t.Helper()
	n := 0
	for {
		release, err := u.Acquire(key)
		if err != nil {
			if !errors.Is(err, limit.ErrRate) {
				t.Fatalf("Acquire = %v, want ErrRate", err)
			}
			return n
		}
		release()
		n++
	}
}

var alice = limit.Key{Tenant: "t1", User: "alice"}

func TestRateAllowsOneBurstThenRefills(t *testing.T) {
	u, c := newUsers(t, limit.UserConfig{Rate: 2, Burst: 3, Inflight: 10, Capacity: 10})
	if got := spend(t, u, alice); got != 3 {
		t.Fatalf("burst = %d, want 3", got)
	}
	c.now = c.now.Add(500 * time.Millisecond)
	if got := spend(t, u, alice); got != 1 {
		t.Fatalf("after half a second = %d, want 1", got)
	}
}

func TestInflightIsCappedAndReleaseIsIdempotent(t *testing.T) {
	u, _ := newUsers(t, limit.UserConfig{Rate: 100, Burst: 100, Inflight: 2, Capacity: 10})
	first, err := u.Acquire(alice)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Acquire(alice); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Acquire(alice); !errors.Is(err, limit.ErrInflight) {
		t.Fatalf("third = %v, want ErrInflight", err)
	}
	first()
	first()
	if _, err := u.Acquire(alice); err != nil {
		t.Fatalf("after release = %v", err)
	}
	if _, err := u.Acquire(alice); !errors.Is(err, limit.ErrInflight) {
		t.Fatalf("a double release freed two slots: %v", err)
	}
}

func TestSameUserInAnotherTenantIsSomeoneElse(t *testing.T) {
	u, _ := newUsers(t, limit.UserConfig{Rate: 1, Burst: 2, Inflight: 10, Capacity: 10})
	spend(t, u, alice)
	if got := spend(t, u, limit.Key{Tenant: "t2", User: "alice"}); got != 2 {
		t.Fatalf("alice of t2 got %d, want her own burst of 2", got)
	}
}

func TestEvictionDropsOnlyIdleEntries(t *testing.T) {
	u, _ := newUsers(t, limit.UserConfig{Rate: 100, Burst: 100, Inflight: 1, Capacity: 2})
	if _, err := u.Acquire(alice); err != nil {
		t.Fatal(err)
	}
	bob, err := u.Acquire(limit.Key{Tenant: "t1", User: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	bob()
	if _, err := u.Acquire(limit.Key{Tenant: "t1", User: "carol"}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Acquire(alice); !errors.Is(err, limit.ErrInflight) {
		t.Fatalf("alice = %v, want ErrInflight: her busy entry was dropped", err)
	}
	if u.Len() != 2 {
		t.Fatalf("len = %d, want 2", u.Len())
	}
	if _, err := u.Acquire(limit.Key{Tenant: "t1", User: "dave"}); err != nil {
		t.Fatal(err)
	}
	if u.Len() != 3 {
		t.Fatalf("len = %d, want 3: busy entries are kept over the capacity", u.Len())
	}
}

func TestComingBackAfterEvictionRefillsOneBurstAtMost(t *testing.T) {
	u, _ := newUsers(t, limit.UserConfig{Rate: 1, Burst: 3, Inflight: 10, Capacity: 1})
	spend(t, u, alice)
	spend(t, u, limit.Key{Tenant: "t1", User: "bob"})
	if got := spend(t, u, alice); got != 3 {
		t.Fatalf("alice after eviction = %d, want one burst of 3", got)
	}
}

func TestConcurrentUsersNeverPassTheirInflightCap(t *testing.T) {
	const keys, inflight = 50, 3
	u, err := limit.NewUsers(limit.UserConfig{Rate: 1_000_000, Burst: 1_000_000, Inflight: inflight, Capacity: 10})
	if err != nil {
		t.Fatal(err)
	}
	var holders [keys]atomic.Int32
	var over atomic.Bool
	var wg sync.WaitGroup
	for g := range 64 {
		wg.Go(func() {
			for i := range 300 {
				k := (g*7 + i) % keys
				release, err := u.Acquire(limit.Key{Tenant: "t", User: strconv.Itoa(k)})
				if err != nil {
					continue
				}
				if holders[k].Add(1) > inflight {
					over.Store(true)
				}
				holders[k].Add(-1)
				release()
			}
		})
	}
	wg.Wait()
	if over.Load() {
		t.Fatal("a user passed its inflight cap: a busy entry was evicted")
	}
	if u.Len() > 10 {
		t.Fatalf("len = %d after every release, want at most the capacity", u.Len())
	}
}

func TestNewUsersRefusesBadConfig(t *testing.T) {
	for _, cfg := range []limit.UserConfig{
		{Burst: 1, Inflight: 1, Capacity: 1}, {Rate: 1, Inflight: 1, Capacity: 1},
		{Rate: 1, Burst: 1, Capacity: 1}, {Rate: 1, Burst: 1, Inflight: 1},
	} {
		if _, err := limit.NewUsers(cfg); err == nil {
			t.Fatalf("%+v accepted", cfg)
		}
	}
}

func TestGateCapsTheNode(t *testing.T) {
	if _, err := limit.NewGate(0); err == nil {
		t.Fatal("zero gate accepted")
	}
	g, err := limit.NewGate(2)
	if err != nil {
		t.Fatal(err)
	}
	entered := 0
	for range 3 {
		if g.Enter() {
			entered++
		}
	}
	if entered != 2 || g.InFlight() != 2 {
		t.Fatalf("gate of 2 let in %d", entered)
	}
	g.Leave()
	if !g.Enter() {
		t.Fatal("gate stayed shut after a leave")
	}
}

func TestGateHoldsUnderConcurrency(t *testing.T) {
	g, err := limit.NewGate(5)
	if err != nil {
		t.Fatal(err)
	}
	var inside, peak atomic.Int32
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			for range 200 {
				if !g.Enter() {
					continue
				}
				n := inside.Add(1)
				for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
				}
				inside.Add(-1)
				g.Leave()
			}
		})
	}
	wg.Wait()
	if peak.Load() > 5 || g.InFlight() != 0 {
		t.Fatalf("peak = %d, in flight = %d", peak.Load(), g.InFlight())
	}
}
