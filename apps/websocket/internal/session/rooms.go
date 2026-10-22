package session

import (
	"github.com/lxzan/gws"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

type roomSink struct {
	c *connection
}

func (s roomSink) Send(frame []byte) { s.c.enqueue(outFrame{opcode: gws.OpcodeBinary, payload: frame}) }

func (s roomSink) Fail() { s.c.closeWith(CodeTryAgainLater) }

func (c *connection) join(claims jwtverify.Claims) {
	member := c.server.interest.Register(claims.Tenant, claims.User, roomSink{c: c})
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		member.Unregister()
		return
	}
	c.member = member
	c.mu.Unlock()
}

func (c *connection) currentMember() *interest.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.member
}

func (c *connection) openRoom(room string) {
	if m := c.currentMember(); m != nil {
		m.Open(room)
	}
}

func (c *connection) closeRoom(room string) {
	if m := c.currentMember(); m != nil {
		m.Close(room)
	}
}
