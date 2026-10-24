package session

import (
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
)

func TestSilentConnectionIsClosedWith1000AfterTheHeartbeatTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		time.Sleep(15*time.Second - time.Millisecond)
		synctest.Wait()
		if !tc.isOpen() {
			t.Fatal("closed before the heartbeat timeout")
		}
		if got := tc.waitClosed(); got != CodeNormal {
			t.Fatalf("close code = %d, want %d", got, CodeNormal)
		}
		if h.closes(CodeNormal) != 1 || h.unauth() != 0 || h.server.stats.open.Load() != 0 {
			t.Fatalf("closes 1000 %d unauth %d open %d", h.closes(CodeNormal), h.unauth(), h.server.stats.open.Load())
		}
	})
}

func TestHeartbeatsWithoutAuthKeepTheConnectionUntilTheGraceCloses4001(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		start := time.Now()
		tc.keepAlive(10*time.Second, 170*time.Second)
		synctest.Wait()
		if !tc.isOpen() || h.unauth() != 1 {
			t.Fatalf("open %v unauth %d before the grace", tc.isOpen(), h.unauth())
		}
		if got := tc.waitClosed(); got != CodeUnauthenticated {
			t.Fatalf("close code = %d, want %d", got, CodeUnauthenticated)
		}
		if elapsed := time.Since(start); elapsed != 3*time.Minute {
			t.Fatalf("closed after %v, want 3m", elapsed)
		}
		if h.unauth() != 0 || h.closes(CodeUnauthenticated) != 1 || h.presence.count() != 0 {
			t.Fatalf("unauth %d closes 4001 %d presence touches %d", h.unauth(), h.closes(CodeUnauthenticated), h.presence.count())
		}
	})
}

func TestRequestsWhileWaitingGetUnauthenticatedWithoutCallingCore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.CommandRate, opts.CommandBurst = 1, 2
		h := newHarness(t, opts)
		tc := h.dial(true)
		tc.history(1)
		tc.send(2, "88")
		tc.history(3)
		want := map[uint64]int32{1: int32(codes.Unauthenticated), 2: int32(codes.Unauthenticated), 3: int32(codes.ResourceExhausted)}
		for range 3 {
			resp := tc.next()
			if resp.GetCode() != want[resp.GetId()] {
				t.Fatalf("response = %v, want code %d", resp, want[resp.GetId()])
			}
			if resp.GetCode() == int32(codes.Unauthenticated) && resp.GetMessage() != messageAuth {
				t.Fatalf("message = %q, want %q", resp.GetMessage(), messageAuth)
			}
		}
		if h.core.calls.Load() != 0 || !tc.isOpen() {
			t.Fatalf("core calls %d open %v", h.core.calls.Load(), tc.isOpen())
		}
		tc.close()
	})
}

func TestOtherFramesWhileWaitingGetAuthRequiredAtMostOncePerSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.openRoom("7")
		tc.typing("7", false)
		tc.closeRoom("7")
		tc.loadRooms()
		tc.watch("lan")
		tc.expectAuthRequired(promptMissing)
		synctest.Wait()
		tc.noFrame()
		time.Sleep(time.Second)
		tc.typing("7", false)
		tc.expectAuthRequired(promptMissing)
		tc.write(unknownFramePayload)
		tc.heartbeat()
		synctest.Wait()
		tc.noFrame()
		if h.loader.calls.Load() != 0 || h.presence.askCount() != 0 || h.typingCount("typing_received_total", "") != 0 {
			t.Fatalf("loads %d asks %d typing %v while waiting", h.loader.calls.Load(), h.presence.askCount(), h.typingCount("typing_received_total", ""))
		}
		if h.server.stats.unknownFrames.Load() != 1 {
			t.Fatalf("unknown frames = %d, want 1", h.server.stats.unknownFrames.Load())
		}
		tc.close()
	})
}

func TestUnauthCountFollowsEveryTransition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		synctest.Wait()
		if h.unauth() != 1 {
			t.Fatalf("unauth after dial = %d, want 1", h.unauth())
		}
		tc.auth(h.token("u1", time.Minute))
		tc.nextFrame()
		if h.unauth() != 0 {
			t.Fatalf("unauth after login = %d, want 0", h.unauth())
		}
		tc.keepAlive(10*time.Second, 80*time.Second)
		tc.expectAuthRequired(promptExpired)
		if h.unauth() != 1 {
			t.Fatalf("unauth after expiry = %d, want 1", h.unauth())
		}
		tc.auth(h.token("u1", time.Hour))
		tc.nextFrame()
		if h.unauth() != 0 {
			t.Fatalf("unauth after a new login = %d, want 0", h.unauth())
		}
		waiting := h.dial(true)
		synctest.Wait()
		if h.unauth() != 1 {
			t.Fatalf("unauth with one waiting connection = %d, want 1", h.unauth())
		}
		waiting.close()
		tc.close()
		if h.unauth() != 0 || h.sessionMetric("unauth_over_cap_total", "", "") != 0 {
			t.Fatalf("unauth after close = %d, over cap %v", h.unauth(), h.sessionMetric("unauth_over_cap_total", "", ""))
		}
	})
}

func TestBackToWaitingOverTheCapIsCountedAndKeepsTheConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.UnauthMax = 1
		h := newHarness(t, opts)
		tc := h.readyClientWith(h.token("u1", time.Minute))
		waiting := h.dial(true)
		for range 8 {
			time.Sleep(10 * time.Second)
			tc.heartbeat()
			waiting.heartbeat()
		}
		tc.expectAuthRequired(promptExpired)
		if !tc.isOpen() || h.unauth() != 2 || h.sessionMetric("unauth_over_cap_total", "", "") != 1 {
			t.Fatalf("open %v unauth %d over cap %v", tc.isOpen(), h.unauth(), h.sessionMetric("unauth_over_cap_total", "", ""))
		}
		tc.close()
		waiting.close()
		if h.unauth() != 0 {
			t.Fatalf("unauth after close = %d, want 0", h.unauth())
		}
	})
}
