package session

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/lxzan/gws"
	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

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

func TestBadTokenGetsAuthRequiredInvalidAndALaterGoodTokenGetsReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth("forged")
		tc.expectAuthRequired(promptInvalid)
		if !tc.isOpen() || h.authCount("invalid") != 1 || h.server.stats.unauth.Load() != 1 {
			t.Fatalf("open %v invalid %v unauth %d", tc.isOpen(), h.authCount("invalid"), h.server.stats.unauth.Load())
		}
		time.Sleep(time.Second)
		tc.auth(h.token("u1", time.Hour))
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("frame after a good token = %v, want ready", f)
		}
		if h.authCount("ok") != 1 || h.server.stats.unauth.Load() != 0 {
			t.Fatalf("ok %v unauth %d", h.authCount("ok"), h.server.stats.unauth.Load())
		}
		tc.close()
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

func TestTokenAcceptedInsideTheLeewayExpiresOnlyAtExpPlusLeeway(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.readyClientWith(h.token("u1", -10*time.Second))
		start := time.Now()
		tc.keepAlive(5*time.Second, 15*time.Second)
		synctest.Wait()
		tc.noFrame()
		tc.expectAuthRequired(promptExpired)
		if elapsed := time.Since(start); elapsed != 20*time.Second || !tc.isOpen() {
			t.Fatalf("auth_required after %v, open %v; want 20s and open", elapsed, tc.isOpen())
		}
		tc.close()
	})
}

func TestTokenExpirySendsAuthRequiredAndTheGraceCloses4001(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		start := time.Now()
		tc := h.readyClientWith(h.token("u1", time.Minute))
		tc.keepAlive(10*time.Second, 80*time.Second)
		tc.expectAuthRequired(promptExpired)
		if elapsed := time.Since(start); elapsed != 90*time.Second || !tc.isOpen() {
			t.Fatalf("auth_required after %v, open %v; want 90s and open", elapsed, tc.isOpen())
		}
		tc.heartbeat()
		tc.keepAlive(5*time.Second, 175*time.Second)
		if got := tc.waitClosed(); got != CodeUnauthenticated {
			t.Fatalf("close code = %d, want %d", got, CodeUnauthenticated)
		}
		if elapsed := time.Since(start); elapsed != 270*time.Second {
			t.Fatalf("closed after %v, want 270s", elapsed)
		}
		if h.server.stats.unauth.Load() != 0 || h.closes(CodeUnauthenticated) != 1 {
			t.Fatalf("unauth %d closes 4001 %d", h.server.stats.unauth.Load(), h.closes(CodeUnauthenticated))
		}
	})
}

func TestRenewalWithTheSameIdentityMovesTheExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		start := time.Now()
		tc := h.readyClientWith(h.token("u1", time.Minute))
		tc.keepAlive(10*time.Second, 50*time.Second)
		tc.auth(h.token("u1", 2*time.Minute))
		tc.keepAlive(10*time.Second, 140*time.Second)
		synctest.Wait()
		tc.noFrame()
		tc.expectAuthRequired(promptExpired)
		if elapsed := time.Since(start); elapsed != 200*time.Second {
			t.Fatalf("auth_required after %v, want 200s", elapsed)
		}
		tc.close()
	})
}

func TestRenewalWithAnotherIdentityOrABadTokenKeepsTheConnectionRunning(t *testing.T) {
	cases := map[string]func(h *harness) string{
		"other user":   func(h *harness) string { return h.verifier.issue("other", "t1", "u2", time.Now().Add(time.Hour)) },
		"other tenant": func(h *harness) string { return h.verifier.issue("other", "t2", "u1", time.Now().Add(time.Hour)) },
		"bad token":    func(*harness) string { return "forged" },
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, testOptions())
				h.loader.set("t1", "u1", interest.Room{ID: 7, Ver: 1})
				tc := h.readyClient("u1")
				tc.auth(token(h))
				tc.expectAuthRequired(promptInvalid)
				h.rooms.Route(roomEvent("msg_created", "message", 7, "7-0-1"))
				if f := tc.nextFrame(); f.GetEvent().GetId() != "7-0-1" {
					t.Fatalf("frame = %v, want event 7-0-1", f)
				}
				tc.history(1)
				tc.next()
				if claims := <-h.core.claims; claims.Tenant != "t1" || claims.User != "u1" {
					t.Fatalf("claims after a refused renewal = %+v", claims)
				}
				if h.authCount("invalid") != 1 || h.interestUsers() != 1 {
					t.Fatalf("invalid %v interest users %v", h.authCount("invalid"), h.interestUsers())
				}
				tc.close()
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
