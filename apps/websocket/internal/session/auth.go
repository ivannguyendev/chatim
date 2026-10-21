package session

import (
	"time"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

func (c *connection) authenticate(token string) {
	claims, err := c.server.verifier.Verify(token)
	if err != nil {
		c.server.authFailed(reasonLabel(jwtverify.ReasonOf(err)))
		c.closeWith(CodeUnauthenticated)
		return
	}
	if !c.accept(claims) {
		c.server.authFailed(reasonIdentityChange)
		c.closeWith(CodeUnauthenticated)
	}
}

func (c *connection) accept(claims jwtverify.Claims) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return true
	}
	if c.authed && (claims.User != c.claims.User || claims.Tenant != c.claims.Tenant) {
		return false
	}
	if !c.authed {
		c.authed = true
		stopTimer(c.authTimer)
		c.server.stats.unauth.Add(-1)
	}
	c.claims = claims
	stopTimer(c.expiry)
	c.expiry = time.AfterFunc(time.Until(c.deadlineOf(claims)), c.tokenExpired)
	return true
}

func (c *connection) deadlineOf(claims jwtverify.Claims) time.Time {
	return claims.ExpiresAt.Add(c.server.opts.JWTLeeway)
}

func (c *connection) authTimedOut() {
	if !c.isAuthed() {
		c.closeWith(CodeAuthTimeout)
	}
}

func (c *connection) tokenExpired() {
	c.mu.Lock()
	valid := time.Now().Before(c.deadlineOf(c.claims))
	c.mu.Unlock()
	if !valid {
		c.closeWith(CodeUnauthenticated)
	}
}

func (c *connection) currentClaims() jwtverify.Claims {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.claims
}
