package slotmap

import (
	"testing"
	"time"
)

func TestResolverRoutesEverySlotToItsLiveOwner(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	for _, id := range []string{"core-a", "core-b"} {
		g.heartbeat(id, coreAddr(id), 5*time.Second)
	}
	owners := g.ownByRendezvous("core-a", "core-b")
	g.refresh(t)
	for s := range uint16(Count) {
		rt := g.routeOf(t, s)
		if want := (Route{Core: owners[s], Addr: coreAddr(owners[s]), Owner: true}); rt != want {
			t.Fatalf("slot %d route = %+v, want %+v", s, rt, want)
		}
	}
	room := uint64(0x5f00aa11bb22cc33)
	if addr, ok := g.r.Addr(room); !ok || addr != coreAddr(owners[Of(room)]) {
		t.Fatalf("Addr(room) = %q, %v; want owner %s of slot %d", addr, ok, owners[Of(room)], Of(room))
	}
}

func TestResolverFallsBackToAPreferredLiveCoreWithoutALiveOwner(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	g.heartbeat("core-a", coreAddr("core-a"), 5*time.Second)
	g.heartbeat("core-b", coreAddr("core-b"), 0)
	g.heartbeat("core-blank", "", 0)
	g.own(7, "core-gone")
	g.own(9, "core-blank")
	g.refresh(t)
	alive := []string{"core-a", "core-b"}
	for _, s := range []uint16{7, 8, 9} {
		if rt := g.routeOf(t, s); rt.Owner || rt.Core != Preferred(s, alive) || rt.Addr != coreAddr(rt.Core) {
			t.Fatalf("slot %d route = %+v, want fallback to %s", s, rt, Preferred(s, alive))
		}
	}

	for s := range uint16(Count) {
		g.own(s, "core-a")
	}
	g.refresh(t)
	if rt := g.routeOf(t, 7); !rt.Owner || rt.Core != "core-a" {
		t.Fatalf("slot 7 route = %+v, want owner core-a", rt)
	}
	g.mr.FastForward(6 * time.Second)
	g.refresh(t)
	for s := range uint16(Count) {
		if rt := g.routeOf(t, s); rt.Owner || rt.Core != "core-b" {
			t.Fatalf("slot %d route = %+v after core-a heartbeat expired, want fallback to core-b", s, rt)
		}
	}
}

func TestResolverHasNoRouteWithoutLiveCores(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	if _, ok := g.r.Addr(1); ok {
		t.Fatal("Addr before the first load must report no route")
	}
	g.own(Of(1), "core-gone")
	g.refresh(t)
	if addr, ok := g.r.Addr(1); ok || addr != "" {
		t.Fatalf("Addr = %q, %v; want no route without live cores", addr, ok)
	}
	if _, ok := g.r.AnyAddr(); ok {
		t.Fatal("AnyAddr must report no route without live cores")
	}
	if _, ok := g.r.Slot(Count); ok {
		t.Fatal("Slot out of range must report no route")
	}
}

func TestResolverFollowsSlotMovesAndKeepsTheLastTableWhileRedisFails(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	g.heartbeat("core-a", coreAddr("core-a"), 0)
	g.heartbeat("core-b", coreAddr("core-b"), 0)
	g.own(3, "core-a")
	g.refresh(t)
	g.own(3, "core-b")
	g.refresh(t)
	if rt := g.routeOf(t, 3); rt.Core != "core-b" || !rt.Owner {
		t.Fatalf("slot 3 route = %+v after moving to core-b", rt)
	}

	g.mr.SetError("LOADING redis is loading")
	if err := g.r.Refresh(t.Context()); err == nil {
		t.Fatal("Refresh with failing redis = nil, want an error")
	}
	if rt := g.routeOf(t, 3); rt.Core != "core-b" {
		t.Fatalf("slot 3 route = %+v while redis fails, want the last table", rt)
	}
	g.mr.SetError("")
	g.own(3, "core-a")
	g.refresh(t)
	if rt := g.routeOf(t, 3); rt.Core != "core-a" {
		t.Fatalf("slot 3 route = %+v after redis recovered", rt)
	}
}

func TestResolverBoundsItsCoreTable(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	for _, id := range coreIDs(MaxCores + 5) {
		g.heartbeat(id, coreAddr(id), 0)
	}
	g.refresh(t)
	g.refresh(t)
	if n := len(g.r.table.Load().cores); n != MaxCores {
		t.Fatalf("table holds %d cores, want at most %d", n, MaxCores)
	}
	for s := range uint16(Count) {
		if rt := g.routeOf(t, s); rt.Owner || rt.Addr != coreAddr(rt.Core) {
			t.Fatalf("slot %d route = %+v, want a fallback route", s, rt)
		}
	}
}

func TestResolverReadsDoNotAllocate(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	g.heartbeat("core-a", coreAddr("core-a"), 0)
	g.ownByRendezvous("core-a")
	g.refresh(t)
	room := uint64(1)
	allocs := testing.AllocsPerRun(1000, func() {
		room++
		_, _ = g.r.Addr(room)
		_, _ = g.r.Slot(uint16(room % Count))
		_, _ = g.r.AnyAddr()
	})
	if allocs != 0 {
		t.Fatalf("reads allocate %.1f times per call, want 0", allocs)
	}
}

func TestAnyAddrRotatesOverLiveCores(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	ids := coreIDs(3)
	for _, id := range ids {
		g.heartbeat(id, coreAddr(id), 0)
	}
	g.refresh(t)
	seen := map[string]int{}
	for range 3 * len(ids) {
		addr, ok := g.r.AnyAddr()
		if !ok {
			t.Fatal("AnyAddr reported no route with live cores")
		}
		seen[addr]++
	}
	for _, id := range ids {
		if seen[coreAddr(id)] != 3 {
			t.Fatalf("AnyAddr picks = %v, want each core 3 times", seen)
		}
	}
}
