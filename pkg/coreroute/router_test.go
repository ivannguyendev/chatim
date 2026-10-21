package coreroute_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/callerid"
	"github.com/ivannguyendev/chatim/pkg/coreroute"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const room = 42

func leasedRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	for _, id := range []string{"core-1", "core-2"} {
		_ = mr.Set(slotmap.CoreKey(id), id+":9000")
		_, _ = mr.ZAdd(slotmap.CoreRegistryKey, slotmap.CoreExpiryScore(time.Now().Add(time.Hour)), id)
	}
	_ = mr.Set(slotmap.SlotKey(slotmap.Of(room)), "core-2")
	return mr
}

func openRouter(t *testing.T, mr *miniredis.Miniredis, net *fakeNet) *coreroute.Router {
	t.Helper()
	r, err := coreroute.Open(t.Context(), coreroute.Config{RedisAddr: mr.Addr(), ClientName: "coreroute-test", Dial: net.dial, Log: quietLog})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func awaitClose(t *testing.T, net *fakeNet, want string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case addr := <-net.closed:
			if addr == want {
				return
			}
		case <-timer.C:
			t.Fatalf("connection to %s not closed within 5s", want)
		}
	}
}

func TestOpenRoutesByTheSlotLeaseAndClosesTwice(t *testing.T) {
	mr := leasedRedis(t)
	net := newFakeNet("core-1:9000", "core-2:9000")
	r, err := coreroute.Open(t.Context(), coreroute.Config{RedisAddr: mr.Addr(), Dial: net.dial, Log: quietLog})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if c, err := r.Room("42"); err != nil || c.Addr != "core-2:9000" || c.Mention == nil {
		t.Fatalf("Room(42) = %+v, %v; want the lease owner core-2", c, err)
	}
	if c, err := r.Any(); err != nil || (c.Addr != "core-1:9000" && c.Addr != "core-2:9000") {
		t.Fatalf("Any = %+v, %v", c, err)
	}
	if _, err := r.Room("nope"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Room(nope) = %v, want InvalidArgument", err)
	}
	if err := errors.Join(r.Close(), r.Close()); err != nil {
		t.Fatalf("Close twice: %v", err)
	}
	if _, closes := net.counts("core-2:9000"); closes != 1 {
		t.Fatalf("core-2 closed %d times, want once", closes)
	}
	r.Trigger()
}

func TestRouterDropsTheOldConnectionWhenACoreChangesAddress(t *testing.T) {
	mr := leasedRedis(t)
	net := newFakeNet("core-1:9000", "core-2:9000", "core-2:9100")
	r := openRouter(t, mr, net)
	if _, err := r.Room("42"); err != nil {
		t.Fatal(err)
	}
	_ = mr.Set(slotmap.CoreKey("core-2"), "core-2:9100")
	if err := r.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, closes := net.counts("core-2:9000"); closes != 1 {
		t.Fatalf("old core-2 address closed %d times, want once", closes)
	}
	if c, err := r.Room("42"); err != nil || c.Addr != "core-2:9100" {
		t.Fatalf("Room(42) = %+v, %v; want the new address", c, err)
	}
}

func TestTriggerReloadsTheTableInTheBackground(t *testing.T) {
	mr := leasedRedis(t)
	net := newFakeNet("core-1:9000", "core-2:9000")
	r := openRouter(t, mr, net)
	if _, err := r.Room("42"); err != nil {
		t.Fatal(err)
	}
	mr.Del(slotmap.CoreKey("core-2"))
	r.Trigger()
	awaitClose(t, net, "core-2:9000")
	if c, err := r.Room("42"); err != nil || c.Addr != "core-1:9000" {
		t.Fatalf("Room(42) = %+v, %v; want the remaining core-1", c, err)
	}
}

func TestRouterReportsNoLiveCore(t *testing.T) {
	mr := leasedRedis(t)
	r := openRouter(t, mr, newFakeNet())
	mr.Del(slotmap.CoreKey("core-1"))
	mr.Del(slotmap.CoreKey("core-2"))
	if err := r.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Room("42"); !errors.Is(err, coreroute.ErrNoRoute) || status.Code(err) != codes.Unavailable {
		t.Fatalf("Room without cores = %v, want ErrNoRoute", err)
	}
}

func TestOpenGivesUpWhenTheSlotTableNeverLoads(t *testing.T) {
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	mr.Close()
	_, err := coreroute.Open(t.Context(), coreroute.Config{RedisAddr: addr, ReadyTimeout: 300 * time.Millisecond, Log: quietLog})
	if err == nil || !strings.Contains(err.Error(), "not loaded") {
		t.Fatalf("Open against a dead redis = %v, want a not-loaded error", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := coreroute.Open(ctx, coreroute.Config{RedisAddr: addr, Log: quietLog}); err == nil {
		t.Fatal("Open with a cancelled context succeeded")
	}
	if _, err := coreroute.Open(t.Context(), coreroute.Config{RedisAddr: addr, ReadyTimeout: -time.Second}); err == nil {
		t.Fatal("Open accepted a negative ready timeout")
	}
}

func TestWithCallerAppendsTheCallerHeaders(t *testing.T) {
	md, _ := metadata.FromOutgoingContext(coreroute.WithCaller(t.Context(), "acme", "alice"))
	if got := md.Get(callerid.TenantHeader); len(got) != 1 || got[0] != "acme" {
		t.Fatalf("tenant = %v", got)
	}
	if got := md.Get(callerid.UserHeader); len(got) != 1 || got[0] != "alice" {
		t.Fatalf("user = %v", got)
	}
}
