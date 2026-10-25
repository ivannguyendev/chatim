package session

import (
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

func TestPresenceIsTouchedAfterAuthOnFramesAndOnPings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		_ = tc.conn.WritePing(nil)
		<-tc.pongs
		synctest.Wait()
		if got := h.presence.count(); got != 0 {
			t.Fatalf("touches before auth = %d, want 0", got)
		}
		tc.auth(h.token("u1", time.Hour))
		synctest.Wait()
		authed := h.presence.count()
		if got := h.presence.last(); authed == 0 || got.tenant != "t1" || got.user != "u1" || !got.at.Equal(time.Now()) {
			t.Fatalf("touches after auth = %d, last %+v; want t1/u1 now", authed, got)
		}
		time.Sleep(5 * time.Second)
		tc.heartbeat()
		synctest.Wait()
		if got := h.presence.count(); got != authed+1 || !h.presence.last().at.Equal(time.Now()) {
			t.Fatalf("touches after a heartbeat = %d, want %d at the frame time", got, authed+1)
		}
		_ = tc.conn.WritePing(nil)
		<-tc.pongs
		synctest.Wait()
		if got := h.presence.count(); got != authed+2 {
			t.Fatalf("touches after a ping = %d, want %d", got, authed+2)
		}
		tc.close()
	})
}

func TestPresenceIsNotTouchedForAFailedAuth(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth("not-issued")
		tc.expectAuthRequired(promptInvalid)
		tc.heartbeat()
		synctest.Wait()
		if got := h.presence.count(); got != 0 {
			t.Fatalf("touches after a failed auth = %d, want 0", got)
		}
		tc.close()
	})
}

func TestNewRequiresPresence(t *testing.T) {
	if _, err := New(testOptions(), newFakeVerifier(t), newFakeCore(), newTestInterest(t), newTestTyping(t, newTestInterest(t)), nil, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("New accepted a nil presence")
	}
}
