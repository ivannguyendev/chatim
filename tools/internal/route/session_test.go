package route_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func TestOpenRoutesBySlotLeasesAndCloses(t *testing.T) {
	mr := miniredis.RunT(t)
	for _, id := range []string{"core-1", "core-2"} {
		_ = mr.Set(slotmap.CoreKey(id), id+":9000")
		_, _ = mr.ZAdd(slotmap.CoreRegistryKey, slotmap.CoreExpiryScore(time.Now().Add(time.Hour)), id)
	}
	const room = 42
	_ = mr.Set(slotmap.SlotKey(slotmap.Of(room)), "core-2")
	net := newFakeNet(map[string]*fakeCore{"core-1:9000": {}, "core-2:9000": {}})
	s, err := route.Open(t.Context(), route.SessionConfig{RedisAddr: mr.Addr(), ClientName: "route-test", Dial: net.dial, Log: quietLog})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if addr, ok := s.Resolver.Addr(room); !ok || addr != "core-2:9000" {
		t.Fatalf("Addr(%d) = %q, %v; want the lease owner core-2:9000", room, addr, ok)
	}
	if _, st, err := s.Client.SendMessage(t.Context(), send("c-1")); err != nil || st.Addr != "core-2:9000" {
		t.Fatalf("SendMessage = %v via %q, want success on core-2", err, st.Addr)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpenGivesUpWhenTheSlotTableNeverLoads(t *testing.T) {
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	mr.Close()
	start := time.Now()
	_, err := route.Open(t.Context(), route.SessionConfig{RedisAddr: addr, ReadyTimeout: 300 * time.Millisecond, Log: quietLog})
	if err == nil || !strings.Contains(err.Error(), "not loaded") {
		t.Fatalf("Open against a dead redis = %v, want a not-loaded error", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Open gave up after %v, want soon after the ready timeout", took)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := route.Open(ctx, route.SessionConfig{RedisAddr: addr, Log: quietLog}); err == nil {
		t.Fatal("Open with a cancelled context succeeded")
	}
	if _, err := route.Open(t.Context(), route.SessionConfig{RedisAddr: addr, ReadyTimeout: -time.Second}); err == nil {
		t.Fatal("Open accepted a negative ready timeout")
	}
}
