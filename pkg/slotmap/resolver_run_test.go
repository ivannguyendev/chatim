package slotmap

import (
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestResolverReloadsOnSlotChangeNotifications(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{Refresh: time.Hour})
	g.heartbeat("core-a", coreAddr("core-a"), 0)
	g.heartbeat("core-b", coreAddr("core-b"), 0)
	g.own(3, "core-a")
	g.start(t)
	g.awaitReady(t)
	if rt := g.routeOf(t, 3); rt.Core != "core-a" {
		t.Fatalf("slot 3 route = %+v, want core-a", rt)
	}
	g.own(3, "core-b")
	if err := g.rdb.Publish(t.Context(), ChangedChannel, "core-b").Err(); err != nil {
		t.Fatalf("publish change: %v", err)
	}
	eventually(t, "reload after a change notification", func() bool { return g.routeOf(t, 3).Core == "core-b" })
}

func TestResolverReloadsPeriodicallyWithoutNotifications(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{Refresh: 20 * time.Millisecond})
	g.heartbeat("core-a", coreAddr("core-a"), 0)
	g.heartbeat("core-b", coreAddr("core-b"), 0)
	g.own(3, "core-a")
	g.start(t)
	g.awaitReady(t)
	g.own(3, "core-b")
	eventually(t, "periodic reload", func() bool { return g.routeOf(t, 3).Core == "core-b" })
}

func TestResolverIsReadyOnlyAfterTheFirstSuccessfulLoad(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{Refresh: time.Hour})
	g.heartbeat("core-a", coreAddr("core-a"), 0)
	g.mr.SetError("LOADING redis is loading")
	g.start(t)
	select {
	case <-g.r.Ready():
		t.Fatal("Ready closed while every load failed")
	case <-time.After(300 * time.Millisecond):
	}
	g.mr.SetError("")
	g.awaitReady(t)
	if addr, ok := g.r.AnyAddr(); !ok || addr != coreAddr("core-a") {
		t.Fatalf("AnyAddr = %q, %v after recovery", addr, ok)
	}
}

func TestResolverServesConcurrentRefreshesAndReads(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	g.heartbeat("core-a", coreAddr("core-a"), 0)
	g.ownByRendezvous("core-a")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 20 {
				if err := g.r.Refresh(t.Context()); err != nil {
					t.Errorf("Refresh: %v", err)
				}
				_, _ = g.r.Addr(uint64(i*100 + j + 1))
			}
		})
	}
	wg.Wait()
	if _, ok := g.r.Addr(1); !ok {
		t.Fatal("no route after concurrent refreshes")
	}
}

func TestNewResolverRejectsInvalidSettings(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = rdb.Close() })
	cases := map[string]struct {
		rdb redis.UniversalClient
		cfg ResolverConfig
	}{
		"no redis":             {nil, ResolverConfig{}},
		"negative refresh":     {rdb, ResolverConfig{Refresh: -time.Second}},
		"negative loadTimeout": {rdb, ResolverConfig{LoadTimeout: -time.Second}},
	}
	for name, c := range cases {
		if _, err := NewResolver(c.rdb, c.cfg, nil); err == nil {
			t.Errorf("%s: NewResolver accepted invalid settings", name)
		}
	}
	r, err := NewResolver(rdb, ResolverConfig{}, nil)
	if err != nil || r.cfg.Refresh != DefaultRefresh || r.cfg.LoadTimeout != DefaultLoadTimeout {
		t.Fatalf("NewResolver defaults = %+v, %v", r, err)
	}
}
