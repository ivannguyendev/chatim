package slotmap

import (
	"testing"
	"time"
)

func TestResolverRoutesEverySlotToItsLiveOwner(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	for _, id := range []string{"chat-a", "chat-b"} {
		g.heartbeat(id, nodeAddr(id), 5*time.Second)
	}
	owners := g.ownByRendezvous("chat-a", "chat-b")
	g.refresh(t)
	for s := range uint16(Count) {
		rt := g.routeOf(t, s)
		if want := (Route{Node: owners[s], Addr: nodeAddr(owners[s]), Owner: true}); rt != want {
			t.Fatalf("slot %d route = %+v, want %+v", s, rt, want)
		}
	}
	room := uint64(0x5f00aa11bb22cc33)
	if addr, ok := g.r.Addr(room); !ok || addr != nodeAddr(owners[Of(room)]) {
		t.Fatalf("Addr(room) = %q, %v; want owner %s of slot %d", addr, ok, owners[Of(room)], Of(room))
	}
}

func TestResolverFallsBackToAPreferredLiveChatNodeWithoutALiveOwner(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	g.heartbeat("chat-a", nodeAddr("chat-a"), 5*time.Second)
	g.heartbeat("chat-b", nodeAddr("chat-b"), 0)
	g.heartbeat("chat-blank", "", 0)
	g.own(7, "chat-gone")
	g.own(9, "chat-blank")
	g.refresh(t)
	alive := []string{"chat-a", "chat-b"}
	for _, s := range []uint16{7, 8, 9} {
		if rt := g.routeOf(t, s); rt.Owner || rt.Node != Preferred(s, alive) || rt.Addr != nodeAddr(rt.Node) {
			t.Fatalf("slot %d route = %+v, want fallback to %s", s, rt, Preferred(s, alive))
		}
	}

	for s := range uint16(Count) {
		g.own(s, "chat-a")
	}
	g.refresh(t)
	if rt := g.routeOf(t, 7); !rt.Owner || rt.Node != "chat-a" {
		t.Fatalf("slot 7 route = %+v, want owner chat-a", rt)
	}
	g.advance(6 * time.Second)
	g.refresh(t)
	for s := range uint16(Count) {
		if rt := g.routeOf(t, s); rt.Owner || rt.Node != "chat-b" {
			t.Fatalf("slot %d route = %+v after chat-a heartbeat expired, want fallback to chat-b", s, rt)
		}
	}
}

func TestResolverHasNoRouteWithoutLiveChatNodes(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	if _, ok := g.r.Addr(1); ok {
		t.Fatal("Addr before the first load must report no route")
	}
	g.own(Of(1), "chat-gone")
	g.refresh(t)
	if addr, ok := g.r.Addr(1); ok || addr != "" {
		t.Fatalf("Addr = %q, %v; want no route without live chat nodes", addr, ok)
	}
	if _, ok := g.r.AnyAddr(); ok {
		t.Fatal("AnyAddr must report no route without live chat nodes")
	}
	if _, ok := g.r.Slot(Count); ok {
		t.Fatal("Slot out of range must report no route")
	}
}

func TestResolverFollowsSlotMovesAndKeepsTheLastTableWhileRedisFails(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	g.heartbeat("chat-a", nodeAddr("chat-a"), 0)
	g.heartbeat("chat-b", nodeAddr("chat-b"), 0)
	g.own(3, "chat-a")
	g.refresh(t)
	g.own(3, "chat-b")
	g.refresh(t)
	if rt := g.routeOf(t, 3); rt.Node != "chat-b" || !rt.Owner {
		t.Fatalf("slot 3 route = %+v after moving to chat-b", rt)
	}

	g.mr.SetError("LOADING redis is loading")
	if err := g.r.Refresh(t.Context()); err == nil {
		t.Fatal("Refresh with failing redis = nil, want an error")
	}
	if rt := g.routeOf(t, 3); rt.Node != "chat-b" {
		t.Fatalf("slot 3 route = %+v while redis fails, want the last table", rt)
	}
	g.mr.SetError("")
	g.own(3, "chat-a")
	g.refresh(t)
	if rt := g.routeOf(t, 3); rt.Node != "chat-a" {
		t.Fatalf("slot 3 route = %+v after redis recovered", rt)
	}
}

func TestResolverBoundsItsChatTable(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	for _, id := range nodeIDs(MaxNodes + 5) {
		g.heartbeat(id, nodeAddr(id), 0)
	}
	g.refresh(t)
	g.refresh(t)
	if n := len(g.r.table.Load().nodes); n != MaxNodes {
		t.Fatalf("table holds %d nodes, want at most %d", n, MaxNodes)
	}
	for s := range uint16(Count) {
		if rt := g.routeOf(t, s); rt.Owner || rt.Addr != nodeAddr(rt.Node) {
			t.Fatalf("slot %d route = %+v, want a fallback route", s, rt)
		}
	}
}

func TestResolverReadsDoNotAllocate(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	g.heartbeat("chat-a", nodeAddr("chat-a"), 0)
	g.ownByRendezvous("chat-a")
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

func TestAnyAddrRotatesOverLiveChatNodes(t *testing.T) {
	g := newResolverRig(t, ResolverConfig{})
	ids := nodeIDs(3)
	for _, id := range ids {
		g.heartbeat(id, nodeAddr(id), 0)
	}
	g.refresh(t)
	seen := map[string]int{}
	for range 3 * len(ids) {
		addr, ok := g.r.AnyAddr()
		if !ok {
			t.Fatal("AnyAddr reported no route with live chat nodes")
		}
		seen[addr]++
	}
	for _, id := range ids {
		if seen[nodeAddr(id)] != 3 {
			t.Fatalf("AnyAddr picks = %v, want each node 3 times", seen)
		}
	}
}
