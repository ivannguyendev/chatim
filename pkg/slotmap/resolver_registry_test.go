package slotmap

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestResolverTakesLiveCoresFromTheRegistry(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	g.heartbeat("core-b", coreAddr("core-b"), 5*time.Second)
	_ = g.mr.Set(CoreKey("core-expired"), coreAddr("core-expired"))
	g.register("core-expired", g.now.Add(-time.Millisecond))
	_ = g.mr.Set(CoreKey("core-edge"), coreAddr("core-edge"))
	g.register("core-edge", g.now)
	_ = g.mr.Set(CoreKey("core-unregistered"), coreAddr("core-unregistered"))
	g.register("core-keyless", g.now.Add(time.Minute))
	for i, id := range []string{"core-expired", "core-edge", "core-unregistered", "core-keyless"} {
		g.own(uint16(i), id)
	}
	g.refresh(t)
	if n := len(g.r.table.Load().cores); n != 1 {
		t.Fatalf("table holds %d cores, want only core-b", n)
	}
	for s := range uint16(Count) {
		if rt := g.routeOf(t, s); rt.Owner || rt.Core != "core-b" {
			t.Fatalf("slot %d route = %+v, want fallback to the one registered live core", s, rt)
		}
	}
	g.advance(5 * time.Second)
	g.refresh(t)
	if _, ok := g.r.AnyAddr(); ok {
		t.Fatal("core-b still routed once its registry score passed")
	}
}

func TestResolverLoadCostIgnoresUnrelatedKeys(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	counter := &commandCounter{}
	g.rdb.AddHook(counter)
	g.heartbeat("core-a", coreAddr("core-a"), 5*time.Second)
	g.ownByRendezvous("core-a")
	g.refresh(t)
	base := counter.during(func() { g.refresh(t) })
	for i := range 100_000 {
		_ = g.mr.Set(fmt.Sprintf("chatim:cid:%d:alice:c%d", i%977+1, i), "7:7")
	}
	crowded := counter.during(func() { g.refresh(t) })
	if crowded != base || base != 3 {
		t.Fatalf("Refresh sent %d commands with 100K unrelated keys and %d without, want the same 3", crowded, base)
	}
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
