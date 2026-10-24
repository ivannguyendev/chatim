package session

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestEleventhConnectionOfAUserIsClosedWith4009WithoutALoadOrAStamp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		var conns []*testClient
		for range 10 {
			conns = append(conns, h.readyClient("u1"))
		}
		synctest.Wait()
		loads, touches := h.loader.calls.Load(), h.presence.count()
		extra := h.dial(true)
		extra.auth(h.token("u1", time.Hour))
		if got := extra.waitClosed(); got != CodeTooManyConnections {
			t.Fatalf("close code = %d, want %d", got, CodeTooManyConnections)
		}
		if h.loader.calls.Load() != loads || h.presence.count() != touches {
			t.Fatalf("loads %d -> %d, touches %d -> %d", loads, h.loader.calls.Load(), touches, h.presence.count())
		}
		if h.closes(CodeTooManyConnections) != 1 || h.authCount("user_limit") != 1 || h.unauth() != 0 {
			t.Fatalf("closes 4009 %d user_limit %v unauth %d", h.closes(CodeTooManyConnections), h.authCount("user_limit"), h.unauth())
		}
		other := h.readyClient("u2")
		conns[0].close()
		conns = append(conns[1:], h.readyClient("u1"), other)
		for _, tc := range conns {
			if !tc.isOpen() {
				t.Fatal("a connection under the limit was closed")
			}
			tc.close()
		}
	})
}
