package slotmap

import (
	"context"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var quietLog = slog.New(slog.DiscardHandler)

type resolverRig struct {
	mr  *miniredis.Miniredis
	rdb *redis.Client
	r   *Resolver
}

func newResolverRig(t *testing.T, cfg ResolverConfig) *resolverRig {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), ContextTimeoutEnabled: true, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	r, err := NewResolver(rdb, cfg, quietLog)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return &resolverRig{mr: mr, rdb: rdb, r: r}
}

func (g *resolverRig) heartbeat(id, addr string, ttl time.Duration) {
	_ = g.mr.Set(CoreKey(id), addr)
	if ttl > 0 {
		g.mr.SetTTL(CoreKey(id), ttl)
	}
}

func (g *resolverRig) own(slot uint16, id string) { _ = g.mr.Set(SlotKey(slot), id) }

func (g *resolverRig) ownByRendezvous(ids ...string) map[uint16]string {
	owners := make(map[uint16]string, Count)
	for s := range uint16(Count) {
		owners[s] = Preferred(s, ids)
		g.own(s, owners[s])
	}
	return owners
}

func (g *resolverRig) refresh(t *testing.T) {
	t.Helper()
	if err := g.r.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
}

func (g *resolverRig) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned %v after cancel, want nil", err)
		}
	})
}

func (g *resolverRig) awaitReady(t *testing.T) {
	t.Helper()
	select {
	case <-g.r.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("resolver not ready within 5s")
	}
}

func (g *resolverRig) routeOf(t *testing.T, slot uint16) Route {
	t.Helper()
	rt, ok := g.r.Slot(slot)
	if !ok {
		t.Fatalf("slot %d has no route", slot)
	}
	return rt
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within 5s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func coreAddr(id string) string { return id + ":9000" }

func coreIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "core-" + strconv.Itoa(i)
	}
	return ids
}
