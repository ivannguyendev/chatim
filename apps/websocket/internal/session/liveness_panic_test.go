package session

import (
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
)

func TestPingsAloneKeepAnAuthenticatedConnectionAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		for range 8 {
			time.Sleep(5 * time.Second)
			_ = tc.conn.WritePing(nil)
			<-tc.pongs
		}
		synctest.Wait()
		if !tc.isOpen() {
			t.Fatal("connection kept alive by pings was closed")
		}
		tc.close()
	})
}

func TestPongsAloneKeepAnAuthenticatedConnectionAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		for range 8 {
			time.Sleep(5 * time.Second)
			_ = tc.conn.WritePong(nil)
		}
		synctest.Wait()
		if !tc.isOpen() {
			t.Fatal("connection kept alive by pongs was closed")
		}
		tc.close()
	})
}

func TestPingsBeforeAuthDoNotExtendTheAuthTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		start := time.Now()
		for range 4 {
			time.Sleep(time.Second)
			_ = tc.conn.WritePing(nil)
			<-tc.pongs
		}
		if got := tc.waitClosed(); got != CodeAuthTimeout {
			t.Fatalf("close code = %d, want %d", got, CodeAuthTimeout)
		}
		if elapsed := time.Since(start); elapsed != 5*time.Second {
			t.Fatalf("closed after %v, want 5s", elapsed)
		}
	})
}

func TestEarlyTokenTimerRearmsForTheRemainingTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		start := time.Now()
		tc.auth(h.token("u1", time.Minute))
		synctest.Wait()
		c := h.onlyConnection()
		c.mu.Lock()
		c.expiry.Stop()
		c.mu.Unlock()
		c.tokenExpired()
		synctest.Wait()
		if !tc.isOpen() {
			t.Fatal("early timer closed a valid token")
		}
		tc.keepAlive(10*time.Second, 80*time.Second)
		if got := tc.waitClosed(); got != CodeUnauthenticated {
			t.Fatalf("close code = %d, want %d", got, CodeUnauthenticated)
		}
		if elapsed := time.Since(start); elapsed != 90*time.Second {
			t.Fatalf("closed after %v, want 90s", elapsed)
		}
	})
}

func TestPanicInTheWriterClosesOnlyThatConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		armed := &atomic.Bool{}
		broken := h.dialWrapped(true, func(c net.Conn) net.Conn { return &explodingConn{Conn: c, armed: armed} })
		healthy := h.dial(true)
		broken.auth(h.token("u1", time.Hour))
		healthy.auth(h.token("u2", time.Hour))
		synctest.Wait()
		armed.Store(true)
		broken.history(1)
		broken.waitClosed()
		healthy.history(2)
		if resp := healthy.next(); resp.GetId() != 2 || resp.GetCode() != 0 {
			t.Fatalf("healthy response = %v", resp)
		}
		healthy.close()
		if h.server.stats.open.Load() != 0 || h.server.commands.count.Load() != 0 {
			t.Fatalf("open = %d, commands = %d", h.server.stats.open.Load(), h.server.commands.count.Load())
		}
	})
}

func TestNilResponseFromTheExecutorIsAnsweredWithInternal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.core.mu.Lock()
		h.core.nils[1] = true
		h.core.mu.Unlock()
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.history(1)
		resp := tc.next()
		if resp.GetId() != 1 || resp.GetCode() != int32(codes.Internal) || resp.GetMessage() != messageInternal {
			t.Fatalf("response = %v", resp)
		}
		tc.close()
	})
}

func (h *harness) onlyConnection() *connection {
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	if len(h.server.live) != 1 {
		h.t.Fatalf("live connections = %d, want 1", len(h.server.live))
	}
	for c := range h.server.live {
		return c
	}
	return nil
}

type explodingConn struct {
	net.Conn
	armed *atomic.Bool
}

func (c *explodingConn) Write(p []byte) (int, error) {
	if c.armed.CompareAndSwap(true, false) {
		panic("socket exploded")
	}
	return c.Conn.Write(p)
}

func (tc *testClient) keepAlive(every, total time.Duration) {
	for elapsed := time.Duration(0); elapsed+every <= total; elapsed += every {
		time.Sleep(every)
		tc.heartbeat()
	}
}

func (h *harness) closes(code uint16) uint64 {
	return h.server.stats.closes[strconv.FormatUint(uint64(code), 10)].Load()
}
