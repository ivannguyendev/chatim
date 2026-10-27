package session

import (
	"errors"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/pkg/backoff"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

const (
	throttleRetry = time.Second
	keyRetry      = time.Second
)

const (
	promptMissing = chatimv1.AuthRequiredReason_AUTH_REQUIRED_REASON_MISSING
	promptInvalid = chatimv1.AuthRequiredReason_AUTH_REQUIRED_REASON_INVALID
	promptExpired = chatimv1.AuthRequiredReason_AUTH_REQUIRED_REASON_EXPIRED
	promptRevoked = chatimv1.AuthRequiredReason_AUTH_REQUIRED_REASON_REVOKED
)

type renewal int

const (
	renewed renewal = iota
	renewOtherIdentity
	renewWaiting
	renewFinished
	renewBlocked
)

func (c *connection) authenticate(token string, running bool) {
	if running {
		c.renew(token)
		return
	}
	c.login(token)
}

func (c *connection) login(token string) {
	if !c.authGate.Allow() {
		c.retryLater(authThrottled, throttleRetry)
		return
	}
	claims, ok := c.verify(token)
	if !ok {
		return
	}
	if verdict := c.server.revocations.Check(claims); verdict != revocation.Allowed {
		c.blocked(verdict, true)
		return
	}
	c.enter(claims)
}

func (c *connection) verify(token string) (jwtverify.Claims, bool) {
	claims, err := c.server.verifier.Verify(token)
	if err == nil {
		return claims, true
	}
	if jwtverify.ReasonOf(err) == jwtverify.ReasonKeyID && c.server.revocations.UnknownKey(jwtverify.KeyIDOf(token)) {
		c.retryLater(authKeyPending, keyRetry)
		return jwtverify.Claims{}, false
	}
	prompt := c.refused(err)
	c.setReason(prompt)
	c.sendAuthRequired(prompt)
	return jwtverify.Claims{}, false
}

func (c *connection) blocked(verdict revocation.Verdict, waiting bool) {
	if verdict == revocation.TenantRevoked {
		c.server.stats.authed(authRevoked)
		c.closeWith(CodeTenantRevoked)
		return
	}
	c.server.authRefused(authRevoked, "revoked")
	if waiting {
		c.setReason(promptRevoked)
	}
	c.sendAuthRequired(promptRevoked)
}

func (c *connection) refused(err error) chatimv1.AuthRequiredReason {
	reason := jwtverify.ReasonOf(err)
	if reason == jwtverify.ReasonExpired {
		c.server.authRefused(authExpired, reasonLabel(reason))
		return promptExpired
	}
	c.server.authRefused(authInvalid, reasonLabel(reason))
	return promptInvalid
}

func (c *connection) enter(claims jwtverify.Claims) {
	if !c.sameIdentity(claims) {
		c.server.authRefused(authInvalid, reasonIdentityChange)
		c.setReason(promptInvalid)
		c.sendAuthRequired(promptInvalid)
		return
	}
	if !c.server.logins.Allow() {
		c.retryLater(authRate, backoff.Between(c.server.opts.AuthRetryMin, c.server.opts.AuthRetryMax))
		return
	}
	gen := c.generation()
	member, err := c.server.interest.Register(claims.Tenant, claims.User, roomSink{c: c})
	if err != nil {
		c.registerFailed(err)
		return
	}
	started, verdict := c.startRunning(claims, member, gen)
	if verdict != revocation.Allowed {
		c.blocked(verdict, true)
		return
	}
	if started {
		c.server.stats.authed(authOK)
	}
}

func (c *connection) registerFailed(err error) {
	if errors.Is(err, interest.ErrTooManyConnections) {
		c.server.stats.authed(authUserLimit)
		c.closeWith(CodeTooManyConnections)
		return
	}
	c.server.logger.Error("websocket interest register failed", "error", err)
	c.closeWith(CodeInternalError)
}

func (c *connection) startRunning(claims jwtverify.Claims, member *interest.Conn, gen uint64) (bool, revocation.Verdict) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished || c.running || c.authGen != gen {
		member.Unregister()
		return false, revocation.Allowed
	}
	if verdict := c.server.revocations.Check(claims); verdict != revocation.Allowed {
		member.Unregister()
		return false, verdict
	}
	c.running = true
	c.identified = true
	c.authGen++
	c.claims = claims
	c.member = member
	c.watch = newPresenceWatch(&c.server.presenceStats)
	stopTimer(c.grace)
	c.server.stats.unauth.Add(-1)
	c.armExpiry(time.Until(c.deadlineOf(claims)))
	return true, revocation.Allowed
}

func (c *connection) renew(token string) {
	claims, ok := c.verify(token)
	if !ok {
		return
	}
	verdict := c.server.revocations.Check(claims)
	switch c.renewWith(claims, verdict) {
	case renewed:
		c.server.stats.authed(authOK)
	case renewOtherIdentity:
		c.server.authRefused(authInvalid, reasonIdentityChange)
		c.sendAuthRequired(promptInvalid)
	case renewWaiting:
		if verdict != revocation.Allowed {
			c.blocked(verdict, true)
			return
		}
		c.enter(claims)
	case renewBlocked:
		c.blocked(verdict, false)
	case renewFinished:
	}
}

func (c *connection) renewWith(claims jwtverify.Claims, verdict revocation.Verdict) renewal {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.finished:
		return renewFinished
	case !c.running:
		return renewWaiting
	case claims.User != c.claims.User || claims.Tenant != c.claims.Tenant:
		return renewOtherIdentity
	case verdict != revocation.Allowed:
		return renewBlocked
	}
	c.claims = claims
	c.armExpiry(time.Until(c.deadlineOf(claims)))
	return renewed
}

func (c *connection) armExpiry(after time.Duration) {
	stopTimer(c.expiry)
	gen := c.authGen
	c.expiry = time.AfterFunc(after, func() { c.tokenExpired(gen) })
}

func (c *connection) deadlineOf(claims jwtverify.Claims) time.Time {
	return claims.ExpiresAt.Add(c.server.opts.JWTLeeway)
}

func (c *connection) sameIdentity(claims jwtverify.Claims) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.identified || (claims.Tenant == c.claims.Tenant && claims.User == c.claims.User)
}

func (c *connection) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authGen
}

func (c *connection) currentClaims() jwtverify.Claims {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.claims
}

func (c *connection) retryLater(result authResult, after time.Duration) {
	c.server.stats.authed(result)
	var hint uint32
	if ms := after.Milliseconds(); ms > 0 && ms <= math.MaxUint32 {
		hint = uint32(ms)
	}
	c.sendFrame(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_RetryLater{RetryLater: &chatimv1.RetryLaterFrame{RetryAfterMs: hint}}}, sendControl)
}
