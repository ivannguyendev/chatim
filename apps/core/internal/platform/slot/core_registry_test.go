package slot

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

var redisEpoch = time.UnixMilli(1_700_000_000_000)

func TestHeartbeatRegistersTheCoreUntilItsTTL(t *testing.T) {
	mr, rdb := newRedis(t)
	a := newManager(t, rdb, "core-a")
	stepAll(t, a)
	score, err := mr.ZScore(slotmap.CoreRegistryKey, "core-a")
	if want := slotmap.CoreExpiryScore(redisEpoch.Add(a.cfg.HeartbeatTTL)); err != nil || score != want {
		t.Fatalf("registry score = %v, %v; want heartbeat expiry %v", score, err, want)
	}
	if addr, _ := mr.Get(slotmap.CoreKey("core-a")); addr != "core-a:9000" || mr.TTL(slotmap.CoreKey("core-a")) != a.cfg.HeartbeatTTL {
		t.Fatalf("core key = %q with ttl %v, want the advertise address for %v", addr, mr.TTL(slotmap.CoreKey("core-a")), a.cfg.HeartbeatTTL)
	}
}

func TestExpiredCoreIsPrunedAndNotAlive(t *testing.T) {
	mr, rdb := newRedis(t)
	a, b := newManager(t, rdb, "core-a"), newManager(t, rdb, "core-b")
	for range 3 {
		stepAll(t, a, b)
	}
	assertShares(t, 512, 512, a, b)
	advance(t, mr, rdb, b.cfg.HeartbeatTTL-time.Millisecond)
	stepAll(t, a)
	if !registered(mr, "core-b") || len(a.Owned()) != 512 {
		t.Fatalf("core-b dropped %v before its heartbeat expired; core-a owns %d", time.Millisecond, len(a.Owned()))
	}
	advance(t, mr, rdb, time.Millisecond)
	stepAll(t, a)
	if members, _ := mr.ZMembers(slotmap.CoreRegistryKey); !slices.Equal(members, []string{"core-a"}) {
		t.Fatalf("registry = %v once core-b expired, want only core-a", members)
	}
	assertPartition(t, mr, a)
}

func TestStepCommandsIgnoreUnrelatedKeys(t *testing.T) {
	mr, rdb := newRedis(t)
	counter := &commandCounter{}
	rdb.AddHook(counter)
	a := newManager(t, rdb, "core-a")
	stepAll(t, a)
	base := counter.during(func() { stepAll(t, a) })
	for i := range 100_000 {
		if err := mr.Set(fmt.Sprintf("chatim:cid:%d:alice:c%d", i%977+1, i), "7:7"); err != nil {
			t.Fatalf("seed unrelated key: %v", err)
		}
	}
	crowded := counter.during(func() { stepAll(t, a) })
	if crowded != base || base != 2 {
		t.Fatalf("Step sent %d commands with 100K unrelated keys and %d without, want the same 2", crowded, base)
	}
}

func advance(t *testing.T, mr *miniredis.Miniredis, rdb *redis.Client, d time.Duration) {
	t.Helper()
	now, err := rdb.Time(context.Background()).Result()
	if err != nil {
		t.Fatalf("redis TIME: %v", err)
	}
	mr.SetTime(now.Add(d))
	mr.FastForward(d)
}

func registered(mr *miniredis.Miniredis, core string) bool {
	members, _ := mr.ZMembers(slotmap.CoreRegistryKey)
	return slices.Contains(members, core)
}

type commandCounter struct{ n atomic.Int64 }

func (c *commandCounter) DialHook(next redis.DialHook) redis.DialHook { return next }

func (c *commandCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		c.n.Add(1)
		return next(ctx, cmd)
	}
}

func (c *commandCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		c.n.Add(int64(len(cmds)))
		return next(ctx, cmds)
	}
}

func (c *commandCounter) during(f func()) int64 {
	before := c.n.Load()
	f()
	return c.n.Load() - before
}
