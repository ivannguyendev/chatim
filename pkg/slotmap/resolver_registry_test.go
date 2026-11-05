package slotmap

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestResolverTakesLiveChatNodesFromTheRegistry(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	g.heartbeat("chat-b", nodeAddr("chat-b"), 5*time.Second)
	_ = g.mr.Set(NodeKey("chat-expired"), nodeAddr("chat-expired"))
	g.register("chat-expired", g.now.Add(-time.Millisecond))
	_ = g.mr.Set(NodeKey("chat-edge"), nodeAddr("chat-edge"))
	g.register("chat-edge", g.now)
	_ = g.mr.Set(NodeKey("chat-unregistered"), nodeAddr("chat-unregistered"))
	g.register("chat-keyless", g.now.Add(time.Minute))
	for i, id := range []string{"chat-expired", "chat-edge", "chat-unregistered", "chat-keyless"} {
		g.own(uint16(i), id)
	}
	g.refresh(t)
	if n := len(g.r.table.Load().nodes); n != 1 {
		t.Fatalf("table holds %d nodes, want only chat-b", n)
	}
	for s := range uint16(Count) {
		if rt := g.routeOf(t, s); rt.Owner || rt.Node != "chat-b" {
			t.Fatalf("slot %d route = %+v, want fallback to the one registered live node", s, rt)
		}
	}
	g.advance(5 * time.Second)
	g.refresh(t)
	if _, ok := g.r.AnyAddr(); ok {
		t.Fatal("chat-b still routed once its registry score passed")
	}
}

func TestResolverLoadCostIgnoresUnrelatedKeys(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	counter := &commandCounter{}
	g.rdb.AddHook(counter)
	g.heartbeat("chat-a", nodeAddr("chat-a"), 5*time.Second)
	g.ownByRendezvous("chat-a")
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
