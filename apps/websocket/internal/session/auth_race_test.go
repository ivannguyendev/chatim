package session

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
)

type blockingInterest struct {
	Interest
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func newBlockingInterest(rooms Interest) *blockingInterest {
	return &blockingInterest{Interest: rooms, entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingInterest) Register(tenant, user string, sink interest.Sink) (*interest.Conn, error) {
	c, err := b.Interest.Register(tenant, user, sink)
	if b.armed.CompareAndSwap(true, false) {
		b.entered <- struct{}{}
		<-b.release
	}
	return c, err
}

func blockingHarness(t *testing.T) (*harness, *blockingInterest) {
	var blocking *blockingInterest
	h := newHarnessWith(t, testOptions(), func(rooms Interest) Interest {
		blocking = newBlockingInterest(rooms)
		return blocking
	})
	return h, blocking
}

func (v *fakeVerifier) holdToken(token string) chan struct{} {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.holds == nil {
		v.holds = map[string]chan struct{}{}
	}
	hold := make(chan struct{})
	v.holds[token] = hold
	return hold
}

func TestNoDeliveryReachesAConnectionOnceItStoppedRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.loader.set("t1", "u1", interest.Room{ID: 7, Ver: 1})
		tc := h.readyClientWith(h.token("u1", time.Minute))
		c := h.onlyConnection()
		c.mu.Lock()
		c.expiry.Stop()
		c.claims.ExpiresAt = time.Now().Add(-time.Hour)
		gen := c.authGen
		c.mu.Unlock()
		if !c.stopRunning(gen) {
			t.Fatal("stopRunning refused a running connection past its deadline")
		}
		h.rooms.Route(roomEvent("msg_created", "message", 7, "7-0-1"))
		synctest.Wait()
		tc.noFrame()
		if h.interestUsers() != 0 || h.unauth() != 1 {
			t.Fatalf("interest users %v unauth %d after stopRunning", h.interestUsers(), h.unauth())
		}
		tc.close()
	})
}

func TestAStaleGenerationDuringTheJoinUnregistersTheNewMember(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, blocking := blockingHarness(t)
		tc, c := h.dialConnection(true)
		blocking.armed.Store(true)
		tc.auth(h.token("u1", time.Hour))
		<-blocking.entered
		c.mu.Lock()
		c.authGen++
		c.mu.Unlock()
		close(blocking.release)
		synctest.Wait()
		if _, running := c.authedClaims(); running || h.interestUsers() != 0 || h.unauth() != 1 || h.authCount("ok") != 0 {
			t.Fatalf("running %v interest users %v unauth %d ok %v", running, h.interestUsers(), h.unauth(), h.authCount("ok"))
		}
		tc.close()
	})
}

func TestFinishDuringTheJoinLeavesNoRegisteredConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, blocking := blockingHarness(t)
		tc, c := h.dialConnection(true)
		blocking.armed.Store(true)
		tc.auth(h.token("u1", time.Hour))
		<-blocking.entered
		h.server.CloseAll(CodeGoingAway)
		c.finish(nil)
		if h.unauth() != 0 {
			t.Fatalf("unauth after finish = %d, want 0", h.unauth())
		}
		close(blocking.release)
		tc.waitClosed()
		synctest.Wait()
		if _, running := c.authedClaims(); running || h.interestUsers() != 0 || h.unauth() != 0 || h.server.stats.open.Load() != 0 {
			t.Fatalf("running %v interest users %v unauth %d open %d", running, h.interestUsers(), h.unauth(), h.server.stats.open.Load())
		}
	})
}

func TestRenewalLandingJustAfterExpiryLogsInAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.readyClientWith(h.token("u1", time.Minute))
		c := h.onlyConnection()
		tc.keepAlive(10*time.Second, 80*time.Second)
		renewal := h.token("u1", time.Hour)
		hold := h.verifier.holdToken(renewal)
		tc.auth(renewal)
		time.Sleep(10 * time.Second)
		tc.expectAuthRequired(promptExpired)
		close(hold)
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("frame after the late renewal = %v, want ready", f)
		}
		synctest.Wait()
		if _, running := c.authedClaims(); !running || c.generation() != 3 || h.interestUsers() != 1 || h.unauth() != 0 {
			t.Fatalf("running %v generation %d interest users %v unauth %d", running, c.generation(), h.interestUsers(), h.unauth())
		}
		if h.authCount("ok") != 2 {
			t.Fatalf("ok logins = %v, want 2", h.authCount("ok"))
		}
		tc.close()
	})
}

func TestALoginAfterExpiryWithAnotherIdentityIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.readyClientWith(h.token("u1", time.Minute))
		tc.keepAlive(10*time.Second, 80*time.Second)
		tc.expectAuthRequired(promptExpired)
		tc.auth(h.verifier.issue("other", "t2", "u1", time.Now().Add(time.Hour)))
		tc.expectAuthRequired(promptInvalid)
		tc.openRoom("7")
		tc.expectAuthRequired(promptInvalid)
		if h.authCount("invalid") != 1 || h.interestUsers() != 0 || h.unauth() != 1 || !tc.isOpen() {
			t.Fatalf("invalid %v interest users %v unauth %d open %v", h.authCount("invalid"), h.interestUsers(), h.unauth(), tc.isOpen())
		}
		time.Sleep(time.Second)
		tc.auth(h.verifier.issue("someone", "t1", "u2", time.Now().Add(time.Hour)))
		tc.expectAuthRequired(promptInvalid)
		time.Sleep(time.Second)
		tc.auth(h.token("u1", time.Hour))
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("frame after the same identity = %v, want ready", f)
		}
		if h.authCount("invalid") != 2 || h.authCount("ok") != 2 {
			t.Fatalf("invalid %v ok %v", h.authCount("invalid"), h.authCount("ok"))
		}
		tc.close()
	})
}
