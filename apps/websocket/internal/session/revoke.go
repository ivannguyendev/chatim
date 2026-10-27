package session

import "github.com/ivannguyendev/chatim/pkg/revocation"

func (s *Server) Revoke(added revocation.Added) {
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
	if verdict == revocation.SessionRevoked {
		c.toWaiting(promptRevoked)
	}
	c.mu.Unlock()
	switch verdict {
	case revocation.SessionRevoked:
		c.server.stats.authRequiredRevoked.Add(1)
		c.sendAuthRequired(promptRevoked)
	case revocation.TenantRevoked:
		c.closeWith(CodeTenantRevoked)
	case revocation.Allowed:
	}
}
