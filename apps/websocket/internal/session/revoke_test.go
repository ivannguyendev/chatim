package session

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func (h *harness) sessionToken(token, session string, issued time.Time) string {
	return h.verifier.issueClaims(token, jwtverify.Claims{Tenant: "t1", User: "u1", Session: session, IssuedAt: issued, ExpiresAt: issued.Add(time.Hour)})
}

func (h *harness) revokeSession(session string) {
	added := h.revoked.list.Merge(revocation.Snapshot{Sessions: []revocation.Session{{ID: session, Tenant: "t1", ExpiresAt: time.Now().Add(16 * time.Minute)}}}, time.Now())
	h.server.Revoke(added)
}

func (h *harness) revokeTenant(before time.Time) {
	added := h.revoked.list.Merge(revocation.Snapshot{Tenants: []revocation.Tenant{{ID: "t1", RevokedBefore: before, ExpiresAt: before.Add(16 * time.Minute)}}}, time.Now())
	h.server.Revoke(added)
}

func TestRevokedSessionSendsTheRunningConnectionBackToWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.loader.set("t1", "u1", interest.Room{ID: 7, Ver: 1})
		tc := h.readyClientWith(h.sessionToken("phone-token", "phone", time.Now()))
		h.revokeSession("phone")
		tc.expectAuthRequired(promptRevoked)
		synctest.Wait()
		if h.interestUsers() != 0 || h.sessionMetric("auth_required_total", "reason", "revoked") != 1 {
			t.Fatalf("interest users %v, auth_required revoked %v", h.interestUsers(), h.sessionMetric("auth_required_total", "reason", "revoked"))
		}
		h.rooms.Route(roomEvent("msg_created", "message", 7, "7-0-1"))
		tc.heartbeat()
		synctest.Wait()
		tc.noFrame()
		time.Sleep(time.Second)
		tc.auth(h.sessionToken("laptop-token", "laptop", time.Now()))
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("frame after a token of another session = %v, want ready", f)
		}
		tc.close()
	})
}

func TestRevokedSessionIsRefusedAtLoginAndRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.revokeSession("phone")
		waiting := h.dial(true)
		waiting.auth(h.sessionToken("phone-token", "phone", time.Now()))
		waiting.expectAuthRequired(promptRevoked)
		if !waiting.isOpen() || h.authCount("revoked") != 1 {
			t.Fatalf("open %v revoked auths %v", waiting.isOpen(), h.authCount("revoked"))
		}
		running := h.readyClientWith(h.sessionToken("laptop-token", "laptop", time.Now()))
		running.auth("phone-token")
		running.expectAuthRequired(promptRevoked)
		synctest.Wait()
		if h.interestUsers() != 0 {
			t.Fatal("renewing with a revoked session left the connection running")
		}
		waiting.close()
		running.close()
	})
}

func TestTenantRevokeClosesConnectionsIssuedBeforeIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		now := time.Now()
		old := h.readyClientWith(h.sessionToken("old-token", "old", now.Add(-2*time.Minute)))
		revokedAt := now.Add(-time.Minute)
		fresh := h.readyClientWith(h.sessionToken("fresh-token", "fresh", now))
		h.revokeTenant(revokedAt)
		if got := old.waitClosed(); got != CodeTenantRevoked {
			t.Fatalf("old connection closed with %d, want %d", got, CodeTenantRevoked)
		}
		synctest.Wait()
		if !fresh.isOpen() {
			t.Fatal("a connection issued after the tenant revoke was closed")
		}
		late := h.dial(true)
		late.auth("old-token")
		if got := late.waitClosed(); got != CodeTenantRevoked {
			t.Fatalf("login with a token issued before the revoke closed with %d", got)
		}
		fresh.close()
	})
}

func TestLoginRacingARevokeNeverRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		token := h.sessionToken("phone-token", "phone", time.Now())
		release := h.verifier.holdToken(token)
		tc := h.dial(true)
		tc.auth(token)
		synctest.Wait()
		h.revokeSession("phone")
		close(release)
		tc.expectAuthRequired(promptRevoked)
		synctest.Wait()
		if h.interestUsers() != 0 {
			t.Fatal("a login that raced the revoke reached running")
		}
		tc.close()
	})
}

func TestUnknownKeyAsksToRetryUntilKeysAreFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.verifier.fail("new-kid-token", keyIDError(t))
		h.revoked.unknown.Store(true)
		tc := h.dial(true)
		tc.auth("new-kid-token")
		if wait := tc.expectRetryLater(); wait != time.Second {
			t.Fatalf("retry after %v, want 1s", wait)
		}
		h.revoked.unknown.Store(false)
		time.Sleep(time.Second)
		tc.auth("new-kid-token")
		tc.expectAuthRequired(promptInvalid)
		if h.revoked.asked.Load() != 2 || h.authCount("key_pending") != 1 {
			t.Fatalf("asked %d key_pending %v", h.revoked.asked.Load(), h.authCount("key_pending"))
		}
		tc.close()
	})
}

func keyIDError(t *testing.T) error {
	t.Helper()
	v, err := jwtverify.New(jwtverify.Config{JWKS: []byte(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}]}`), Issuer: "issuer", Audience: "audience"})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	_, err = v.Verify("eyJhbGciOiJFZERTQSIsImtpZCI6Im5ldyJ9.e30.c2ln")
	if jwtverify.ReasonOf(err) != jwtverify.ReasonKeyID {
		t.Fatalf("want an unknown kid error, got %v", err)
	}
	return err
}

func TestRenewRacingARevokeNeverInstallsTheRevokedSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.readyClientWith(h.sessionToken("laptop-token", "laptop", time.Now()))
		renewal := h.sessionToken("phone-token", "phone", time.Now())
		release := h.verifier.holdToken(renewal)
		tc.auth(renewal)
		synctest.Wait()
		h.revokeSession("phone")
		close(release)
		tc.expectAuthRequired(promptRevoked)
		synctest.Wait()
		if h.interestUsers() != 0 {
			t.Fatal("a renewal that raced the revoke kept the connection running")
		}
		tc.close()
	})
}

func TestRevokeLeavesWaitingConnectionsAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		synctest.Wait()
		h.revokeSession("phone")
		synctest.Wait()
		tc.noFrame()
		if !tc.isOpen() || h.unauth() != 1 {
			t.Fatalf("open %v unauth %d", tc.isOpen(), h.unauth())
		}
		tc.close()
	})
}

func TestRevokedConnectionClosesAfterTheAuthGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		h := newHarness(t, opts)
		tc := h.readyClientWith(h.sessionToken("phone-token", "phone", time.Now()))
		h.revokeSession("phone")
		tc.expectAuthRequired(promptRevoked)
		synctest.Wait()
		if h.unauth() != 1 {
			t.Fatalf("unauth = %d after the revoke, want 1", h.unauth())
		}
		tc.keepAlive(opts.HeartbeatTimeout/2, opts.AuthGrace+opts.HeartbeatTimeout)
		if got := tc.waitClosed(); got != CodeUnauthenticated {
			t.Fatalf("closed with %d, want %d after the grace", got, CodeUnauthenticated)
		}
	})
}
