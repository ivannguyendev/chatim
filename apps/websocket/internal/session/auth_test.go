package session

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/lxzan/gws"
	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestConnectionWithoutAuthIsClosedWith4002AfterTheAuthTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		time.Sleep(5*time.Second - time.Millisecond)
		synctest.Wait()
		if !tc.isOpen() {
			t.Fatal("closed before the auth timeout")
		}
		if got := tc.waitClosed(); got != CodeAuthTimeout {
			t.Fatalf("close code = %d, want %d", got, CodeAuthTimeout)
		}
		if h.closes(CodeAuthTimeout) != 1 || h.server.stats.unauth.Load() != 0 || h.server.stats.open.Load() != 0 {
			t.Fatalf("stats after close: closes=%d unauth=%d open=%d", h.closes(CodeAuthTimeout), h.server.stats.unauth.Load(), h.server.stats.open.Load())
		}
	})
}

func TestFrameOtherThanAuthBeforeAuthIsClosedWith1008(t *testing.T) {
	cases := map[string]func(tc *testClient){
		"heartbeat": func(tc *testClient) { tc.heartbeat() },
		"request":   func(tc *testClient) { tc.history(1) },
		"unknown":   func(tc *testClient) { tc.write(unknownFramePayload) },
	}
	for name, send := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, testOptions())
				tc := h.dial(true)
				send(tc)
				if got := tc.waitClosed(); got != CodePolicyViolation {
					t.Fatalf("close code = %d, want %d", got, CodePolicyViolation)
				}
				if h.core.calls.Load() != 0 {
					t.Fatal("core was called before auth")
				}
			})
		})
	}
}

var unknownFramePayload = []byte{0x98, 0x06, 0x01}

func TestUndecodableFrameIsClosedWith1008(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.write([]byte{0x52, 0x05, 0x01})
		if got := tc.waitClosed(); got != CodePolicyViolation {
			t.Fatalf("close code = %d, want %d", got, CodePolicyViolation)
		}
	})
}

func TestTextFrameIsClosedWith1003(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		_ = tc.conn.WriteMessage(gws.OpcodeText, []byte("hello"))
		if got := tc.waitClosed(); got != CodeUnsupportedData {
			t.Fatalf("close code = %d, want %d", got, CodeUnsupportedData)
		}
		if h.closes(CodeUnsupportedData) != 1 {
			t.Fatal("1003 close not counted")
		}
	})
}

func TestFrameOverTheLimitIsClosedWith1009(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.write(make([]byte, testOptions().FrameMax+1))
		tc.waitClosed()
		if h.closes(CodeTooLarge) != 1 {
			t.Fatalf("1009 closes = %d, want 1", h.closes(CodeTooLarge))
		}
	})
}

func TestBadTokenIsClosedWith4001AndCountedByReason(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth("forged")
		if got := tc.waitClosed(); got != CodeUnauthenticated {
			t.Fatalf("close code = %d, want %d", got, CodeUnauthenticated)
		}
		if got := h.server.stats.authFailures["malformed"].Load(); got != 1 {
			t.Fatalf("malformed auth failures = %d, want 1", got)
		}
		if h.server.stats.unauth.Load() != 0 {
			t.Fatal("unauthenticated gauge not released")
		}
	})
}

func TestSilenceAfterAuthClosesTheConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.keepAlive(14*time.Second, 28*time.Second)
		time.Sleep(15*time.Second - time.Millisecond)
		synctest.Wait()
		if !tc.isOpen() {
			t.Fatal("closed before the heartbeat timeout")
		}
		tc.waitClosed()
		if h.closes(CodeNormal) != 1 {
			t.Fatalf("heartbeat timeout closes = %d, want 1", h.closes(CodeNormal))
		}
		if h.server.stats.unauth.Load() != 0 {
			t.Fatal("unauthenticated gauge not released after auth")
		}
	})
}

func TestTokenAcceptedInsideTheLeewayIsNotClosedAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth(h.token("u1", -10*time.Second))
		tc.keepAlive(5*time.Second, 15*time.Second)
		time.Sleep(5*time.Second - time.Millisecond)
		synctest.Wait()
		if !tc.isOpen() {
			t.Fatal("closed before exp + leeway")
		}
		if got := tc.waitClosed(); got != CodeUnauthenticated {
			t.Fatalf("close code = %d, want %d", got, CodeUnauthenticated)
		}
	})
}

func TestTokenExpiryPlusLeewayClosesWith4001(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		start := time.Now()
		tc.auth(h.token("u1", time.Minute))
		tc.keepAlive(10*time.Second, 80*time.Second)
		if got := tc.waitClosed(); got != CodeUnauthenticated {
			t.Fatalf("close code = %d, want %d", got, CodeUnauthenticated)
		}
		if elapsed := time.Since(start); elapsed != 90*time.Second {
			t.Fatalf("closed after %v, want 90s", elapsed)
		}
	})
}

func TestRenewalWithTheSameIdentityKeepsTheConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		start := time.Now()
		tc.auth(h.token("u1", time.Minute))
		tc.keepAlive(10*time.Second, 50*time.Second)
		tc.auth(h.token("u1", 2*time.Minute))
		tc.keepAlive(10*time.Second, 140*time.Second)
		if !tc.isOpen() {
			t.Fatal("renewed connection closed")
		}
		if got := tc.waitClosed(); got != CodeUnauthenticated {
			t.Fatalf("close code = %d, want %d", got, CodeUnauthenticated)
		}
		if elapsed := time.Since(start); elapsed != 200*time.Second {
			t.Fatalf("closed after %v, want 200s", elapsed)
		}
	})
}

func TestRenewalWithAnotherIdentityIsClosedWith4001(t *testing.T) {
	cases := map[string]struct{ tenant, user string }{
		"other user":   {tenant: "t1", user: "u2"},
		"other tenant": {tenant: "t2", user: "u1"},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, testOptions())
				tc := h.dial(true)
				tc.auth(h.token("u1", time.Hour))
				tc.auth(h.verifier.issue("other", tt.tenant, tt.user, time.Now().Add(time.Hour)))
				if got := tc.waitClosed(); got != CodeUnauthenticated {
					t.Fatalf("close code = %d, want %d", got, CodeUnauthenticated)
				}
				if got := h.server.stats.authFailures[reasonIdentityChange].Load(); got != 1 {
					t.Fatalf("identity change failures = %d, want 1", got)
				}
			})
		})
	}
}

func TestFramesAfterTheServerChoseToCloseAreIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(false)
		tc.auth(h.token("u1", time.Hour))
		tc.history(1)
		synctest.Wait()
		h.server.CloseAll(CodeUnauthenticated)
		tc.history(2)
		synctest.Wait()
		if got := h.core.calls.Load(); got != 1 {
			t.Fatalf("core calls = %d, want 1", got)
		}
		<-tc.serverDone
		_ = tc.raw.Close()
	})
}

func TestCommandsRunWithTheClaimsOfTheLatestToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Minute))
		renewed := time.Now().Add(time.Hour)
		tc.auth(h.verifier.issue("renewed", "t1", "u1", renewed))
		tc.history(1)
		if resp := tc.next(); resp.GetCode() != int32(codes.OK) {
			t.Fatalf("response code = %d", resp.GetCode())
		}
		claims := <-h.core.claims
		if claims.Tenant != "t1" || claims.User != "u1" || !claims.ExpiresAt.Equal(renewed) {
			t.Fatalf("claims = %+v", claims)
		}
		tc.close()
	})
}

func TestUnknownFrameAfterAuthIsIgnoredAndCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.write(unknownFramePayload)
		tc.request(&chatimv1.ClientRequest{Id: 7})
		resp := tc.next()
		if resp.GetId() != 7 || resp.GetCode() != int32(codes.Unimplemented) {
			t.Fatalf("response = %v", resp)
		}
		if h.server.stats.unknownFrames.Load() != 1 || h.server.stats.rejected[rejectUnimplemented].Load() != 1 {
			t.Fatalf("unknown frames = %d, unimplemented = %d", h.server.stats.unknownFrames.Load(), h.server.stats.rejected[rejectUnimplemented].Load())
		}
		tc.close()
	})
}

func TestPingIsAnsweredWithPong(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		_ = tc.conn.WritePing([]byte("p"))
		<-tc.pongs
		tc.close()
	})
}
