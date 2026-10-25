package session

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestExpiredTokenWhileWaitingGetsAuthRequiredExpiredAndBecomesTheLastReason(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.verifier.bad = expiredTokenError(t)
		tc := h.dial(true)
		tc.auth("old")
		tc.expectAuthRequired(promptExpired)
		tc.openRoom("7")
		tc.expectAuthRequired(promptExpired)
		if h.authCount("expired") != 1 || h.authCount("invalid") != 0 || !tc.isOpen() {
			t.Fatalf("expired %v invalid %v open %v", h.authCount("expired"), h.authCount("invalid"), tc.isOpen())
		}
		tc.close()
	})
}

func TestSecondAuthWithinASecondGetsAOneSecondRetryLaterWithoutVerifying(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth("forged")
		tc.auth(h.token("u1", time.Hour))
		tc.expectAuthRequired(promptInvalid)
		if after := tc.expectRetryLater(); after != time.Second {
			t.Fatalf("retry_later %v, want 1s", after)
		}
		if got := h.verifier.calls.Load(); got != 1 {
			t.Fatalf("verify calls = %d, want 1", got)
		}
		if h.authCount("throttled") != 1 || h.authCount("rate") != 0 || h.authCount("invalid") != 1 {
			t.Fatalf("throttled %v rate %v invalid %v", h.authCount("throttled"), h.authCount("rate"), h.authCount("invalid"))
		}
		time.Sleep(time.Second)
		tc.auth(h.token("u1", time.Hour))
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("frame after a second = %v, want ready", f)
		}
		tc.close()
	})
}

func TestEmptyNodeLimiterGivesRetryLaterAndALaterAuthSucceeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.AuthRate, opts.AuthBurst = 1, 1
		h := newHarness(t, opts)
		first := h.readyClient("a")
		tc := h.dial(true)
		tc.auth(h.token("b", time.Hour))
		if after := tc.expectRetryLater(); after < 5*time.Second || after > 30*time.Second {
			t.Fatalf("retry_later %v, want within [5s, 30s]", after)
		}
		synctest.Wait()
		if !tc.isOpen() || h.unauth() != 1 || h.interestUsers() != 1 || h.authCount("rate") != 1 {
			t.Fatalf("open %v unauth %d interest users %v rate %v", tc.isOpen(), h.unauth(), h.interestUsers(), h.authCount("rate"))
		}
		time.Sleep(time.Second)
		tc.auth(h.token("b", time.Hour))
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("frame after the limiter refilled = %v, want ready", f)
		}
		if h.authCount("ok") != 2 || h.unauth() != 0 {
			t.Fatalf("ok %v unauth %d", h.authCount("ok"), h.unauth())
		}
		first.close()
		tc.close()
	})
}

func TestGarbageTokensDoNotStarveAValidLogin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.AuthRate, opts.AuthBurst = 1, 1
		h := newHarness(t, opts)
		garbage := make([]*testClient, 20)
		for i := range garbage {
			garbage[i] = h.dial(true)
		}
		var valid *testClient
		for tick := range 100 {
			time.Sleep(20 * time.Millisecond)
			for _, g := range garbage {
				g.auth("garbage")
			}
			if tick == 50 {
				valid = h.dial(true)
				valid.auth(h.token("u1", time.Hour))
			}
		}
		if f := valid.nextFrame(); f.GetReady() == nil {
			t.Fatalf("valid login frame = %v, want ready", f)
		}
		if got := h.verifier.calls.Load(); got > int64(3*len(garbage)+1) {
			t.Fatalf("verify calls = %d, want at most one per garbage connection per second", got)
		}
		if h.authCount("ok") != 1 {
			t.Fatalf("ok logins = %v, want 1", h.authCount("ok"))
		}
		for _, g := range garbage {
			if !g.isOpen() {
				t.Fatal("a garbage connection was closed")
			}
			g.close()
		}
		valid.close()
	})
}

func TestRenewalWithTheSameUserTakesNoNodeLoginToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.AuthRate, opts.AuthBurst = 1, 1
		h := newHarness(t, opts)
		tc := h.readyClient("u1")
		tc.auth(h.token("u1", 2*time.Hour))
		synctest.Wait()
		tc.noFrame()
		if h.authCount("ok") != 2 || h.authCount("rate") != 0 {
			t.Fatalf("ok %v rate %v after a renewal", h.authCount("ok"), h.authCount("rate"))
		}
		other := h.dial(true)
		other.auth(h.token("u2", time.Hour))
		other.expectRetryLater()
		tc.close()
		other.close()
	})
}
