package session

import (
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

func TestExpiryStopsDeliveryUntilANewTokenGivesReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.loader.set("t1", "u1", interest.Room{ID: 7, Ver: 1})
		tc := h.readyClientWith(h.token("u1", time.Minute))
		tc.keepAlive(10*time.Second, 80*time.Second)
		tc.expectAuthRequired(promptExpired)
		synctest.Wait()
		if h.interestUsers() != 0 || h.sessionMetric("auth_required_total", "reason", "expired") != 1 {
			t.Fatalf("interest users %v auth_required expired %v", h.interestUsers(), h.sessionMetric("auth_required_total", "reason", "expired"))
		}
		h.rooms.Route(roomEvent("msg_created", "message", 7, "7-0-1"))
		touches := h.presence.count()
		tc.heartbeat()
		synctest.Wait()
		tc.noFrame()
		if h.presence.count() != touches {
			t.Fatal("presence stamped while waiting")
		}
		tc.auth(h.token("u1", time.Hour))
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("frame after a new token = %v, want ready", f)
		}
		h.rooms.Route(roomEvent("msg_created", "message", 7, "7-0-2"))
		if f := tc.nextFrame(); f.GetEvent().GetId() != "7-0-2" {
			t.Fatalf("frame = %v, want event 7-0-2", f)
		}
		if h.loader.calls.Load() != 2 || h.presence.count() == touches {
			t.Fatalf("loads %d, presence touched again %v", h.loader.calls.Load(), h.presence.count() != touches)
		}
		tc.close()
	})
}

func TestExpiryClearsTheWatchListAndDropsAnAnswerInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.readyClientWith(h.token("u1", time.Minute))
		tc.watch("lan")
		tc.expectPresence("lan")
		tc.keepAlive(10*time.Second, 70*time.Second)
		for range 7 {
			tc.expectPresence("lan")
		}
		hold := h.presence.holdAnswers()
		time.Sleep(9 * time.Second)
		tc.heartbeat()
		synctest.Wait()
		asks := h.presence.askCount()
		tc.expectAuthRequired(promptExpired)
		h.presence.release(hold)
		tc.heartbeat()
		synctest.Wait()
		tc.noFrame()
		if h.presence.askCount() != asks || h.presenceMetric("presence_watching_connections", "") != 0 {
			t.Fatalf("asks %d -> %d, watching %v", asks, h.presence.askCount(), h.presenceMetric("presence_watching_connections", ""))
		}
		tc.close()
	})
}

func TestCommandQueuedBeforeExpiryRunsWithTheClaimsItArrivedWith(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		hold := h.core.hold(1)
		original := time.Now().Add(time.Minute)
		tc := h.readyClientWith(h.token("u1", time.Minute))
		tc.send(1, "88")
		tc.send(2, "88")
		claims := <-h.core.claims
		tc.keepAlive(10*time.Second, 80*time.Second)
		tc.expectAuthRequired(promptExpired)
		tc.history(3)
		if resp := tc.next(); resp.GetId() != 3 || resp.GetCode() != int32(codes.Unauthenticated) {
			t.Fatalf("response while waiting = %v, want 3 unauthenticated", resp)
		}
		hold <- codes.OK
		for range 2 {
			if resp := tc.next(); resp.GetCode() != 0 {
				t.Fatalf("queued response = %v", resp)
			}
		}
		second := <-h.core.claims
		for _, c := range []jwtverify.Claims{claims, second} {
			if c.User != "u1" || !c.ExpiresAt.Equal(original) {
				t.Fatalf("claims = %+v, want u1 expiring %v", c, original)
			}
		}
		if h.core.calls.Load() != 2 {
			t.Fatalf("core calls = %d, want 2", h.core.calls.Load())
		}
		tc.close()
	})
}

func TestExpiryRacingTheJoinLeavesNoRegisteredConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth(h.token("u1", -testOptions().JWTLeeway))
		for f := tc.nextFrame(); f.GetAuthRequired() == nil; f = tc.nextFrame() {
			if f.GetReady() == nil {
				t.Fatalf("frame = %v, want ready or auth_required", f)
			}
		}
		synctest.Wait()
		if h.interestUsers() != 0 || h.unauth() != 1 || !tc.isOpen() {
			t.Fatalf("interest users %v unauth %d open %v", h.interestUsers(), h.unauth(), tc.isOpen())
		}
		tc.close()
	})
}

func TestStaleTimersDoNothingAfterANewLogin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.readyClientWith(h.token("u1", time.Minute))
		c := h.onlyConnection()
		runningGen := c.generation()
		tc.keepAlive(10*time.Second, 80*time.Second)
		tc.expectAuthRequired(promptExpired)
		waitingGen := c.generation()
		tc.auth(h.token("u1", time.Hour))
		tc.nextFrame()
		c.tokenExpired(runningGen)
		c.graceExpired(waitingGen)
		c.graceExpired(0)
		synctest.Wait()
		tc.noFrame()
		if !tc.isOpen() || h.interestUsers() != 1 || h.unauth() != 0 {
			t.Fatalf("open %v interest users %v unauth %d", tc.isOpen(), h.interestUsers(), h.unauth())
		}
		tc.close()
	})
}
