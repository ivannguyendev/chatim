package session

import "github.com/ivannguyendev/chatim/pkg/auth"

func (s *Server) Revoke(added auth.RevocationsAdded) {
	if added.Empty() {
		return
	}
	s.mu.Lock()
	conns := make([]*connection, 0, len(s.live))
	for c := range s.live {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.applyRevocations()
	}
}

func (c *connection) applyRevocations() {
	defer c.recoverPanic()
	c.mu.Lock()
	if c.finished || !c.running {
		c.mu.Unlock()
		return
	}
	verdict := c.server.revocations.Check(c.claims)
	if verdict == auth.SessionRevoked {
		c.toWaiting(promptRevoked)
	}
	c.mu.Unlock()
	switch verdict {
	case auth.SessionRevoked:
		c.server.stats.authRequiredRevoked.Add(1)
		c.sendAuthRequired(promptRevoked)
	case auth.TenantRevoked:
		c.closeWith(CodeTenantRevoked)
	case auth.Allowed:
	}
}
