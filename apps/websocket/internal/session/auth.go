package session

import (
	"errors"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/pkg/auth"
	"github.com/ivannguyendev/chatim/pkg/core/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
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
	claims, ok := c.verify(token, false)
	if !ok {
		return
	}
	if verdict := c.server.revocations.Check(claims); verdict != auth.Allowed {
		c.blocked(verdict, true)
		return
	}
	c.enter(claims)
}

func (c *connection) verify(token string, running bool) (auth.Claims, bool) {
	claims, err := c.server.verifier.Verify(token)
	if err == nil {
		return claims, true
	}
	if auth.ReasonOf(err) == auth.ReasonKeyID && c.server.revocations.UnknownKey(auth.KeyIDOf(token)) {
		c.retryLater(authKeyPending, keyRetry)
		return auth.Claims{}, false
	}
	prompt := c.refused(err)
	if running {
		c.sendAuthRequired(promptInvalid)
		return auth.Claims{}, false
	}
	c.setReason(prompt)
	c.sendAuthRequired(prompt)
	return auth.Claims{}, false
}

func (c *connection) blocked(verdict auth.Verdict, waiting bool) {
	if verdict == auth.TenantRevoked {
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
	reason := auth.ReasonOf(err)
	if reason == auth.ReasonExpired {
		c.server.authRefused(authExpired, reasonLabel(reason))
		return promptExpired
	}
	c.server.authRefused(authInvalid, reasonLabel(reason))
	return promptInvalid
}

func (c *connection) enter(claims auth.Claims) {
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
	if verdict != auth.Allowed {
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

func (c *connection) startRunning(claims auth.Claims, member *interest.Conn, gen uint64) (bool, auth.Verdict) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished || c.running || c.authGen != gen {
		member.Unregister()
		return false, auth.Allowed
	}
	if verdict := c.server.revocations.Check(claims); verdict != auth.Allowed {
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
	return true, auth.Allowed
}

func (c *connection) renew(token string) {
	claims, ok := c.verify(token, true)
	if !ok {
		return
	}
	outcome, verdict := c.renewWith(claims)
	switch outcome {
	case renewed:
		c.server.stats.authed(authOK)
	case renewOtherIdentity:
		c.server.authRefused(authInvalid, reasonIdentityChange)
		c.sendAuthRequired(promptInvalid)
	case renewWaiting:
		if verdict := c.server.revocations.Check(claims); verdict != auth.Allowed {
			c.blocked(verdict, true)
			return
		}
		c.enter(claims)
	case renewBlocked:
		c.blocked(verdict, false)
	case renewFinished:
	}
}

func (c *connection) renewWith(claims auth.Claims) (renewal, auth.Verdict) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.finished:
		return renewFinished, auth.Allowed
	case !c.running:
		return renewWaiting, auth.Allowed
	case claims.User != c.claims.User || claims.Tenant != c.claims.Tenant:
		return renewOtherIdentity, auth.Allowed
	}
	if verdict := c.server.revocations.Check(claims); verdict != auth.Allowed {
		if verdict == auth.SessionRevoked {
			c.toWaiting(promptRevoked)
		}
		return renewBlocked, verdict
	}
	c.claims = claims
	c.armExpiry(time.Until(c.deadlineOf(claims)))
	return renewed, auth.Allowed
}

func (c *connection) armExpiry(after time.Duration) {
	stopTimer(c.expiry)
	gen := c.authGen
	c.expiry = time.AfterFunc(after, func() { c.tokenExpired(gen) })
}

func (c *connection) deadlineOf(claims auth.Claims) time.Time {
	return claims.ExpiresAt.Add(c.server.opts.JWTLeeway)
}

func (c *connection) sameIdentity(claims auth.Claims) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.identified || (claims.Tenant == c.claims.Tenant && claims.User == c.claims.User)
}

func (c *connection) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authGen
}

func (c *connection) currentClaims() auth.Claims {
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
