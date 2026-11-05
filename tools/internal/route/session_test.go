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

const room = 42

func leasedRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	for _, id := range []string{"chat-1", "chat-2"} {
		_ = mr.Set(slotmap.NodeKey(id), id+":9000")
		_, _ = mr.ZAdd(slotmap.NodeRegistryKey, slotmap.NodeExpiryScore(time.Now().Add(time.Hour)), id)
	}
	_ = mr.Set(slotmap.SlotKey(slotmap.Of(room)), "chat-2")
	return mr
}

func TestOpenRoutesBySlotLeasesAndCloses(t *testing.T) {
	mr := leasedRedis(t)
	net := newFakeNet(map[string]*fakeChat{"chat-1:9000": {}, "chat-2:9000": {}})
	s, err := route.Open(t.Context(), route.SessionConfig{RedisAddr: mr.Addr(), ClientName: "route-test", Dial: net.dial, Log: quietLog})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if addr, ok := s.Resolver.Addr(room); !ok || addr != "chat-2:9000" {
		t.Fatalf("Addr(%d) = %q, %v; want the lease owner chat-2:9000", room, addr, ok)
	}
	if _, st, err := s.Client.SendMessage(t.Context(), send("c-1")); err != nil || st.Addr != "chat-2:9000" {
		t.Fatalf("SendMessage = %v via %q, want success on chat-2", err, st.Addr)
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

func TestOpenAuthenticatesWithTheRedisPassword(t *testing.T) {
	mr := leasedRedis(t)
	mr.RequireAuth("route-pw")
	net := newFakeNet(map[string]*fakeChat{"chat-1:9000": {}, "chat-2:9000": {}})
	s, err := route.Open(t.Context(), route.SessionConfig{RedisAddr: mr.Addr(), RedisPassword: "route-pw", Dial: net.dial, Log: quietLog})
	if err != nil {
		t.Fatalf("Open with the password: %v", err)
	}
	if addr, ok := s.Resolver.Addr(room); !ok || addr != "chat-2:9000" {
		t.Errorf("Addr(%d) = %q, %v; want chat-2:9000", room, addr, ok)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err = route.Open(t.Context(), route.SessionConfig{RedisAddr: mr.Addr(), ReadyTimeout: 300 * time.Millisecond, Dial: net.dial, Log: quietLog})
	if err == nil {
		t.Fatal("Open without the password succeeded")
	}
}
