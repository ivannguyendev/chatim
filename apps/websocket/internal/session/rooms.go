package session

import (
	"github.com/lxzan/gws"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type roomSink struct {
	c *connection
}

func (s roomSink) TrySend(frame []byte) bool {
	return s.c.tryEnqueue(outFrame{opcode: gws.OpcodeBinary, payload: frame})
}

func (s roomSink) Offer(frame []byte) bool {
	return s.c.offer(outFrame{opcode: gws.OpcodeBinary, payload: frame})
}

func (s roomSink) Drained() bool { return s.c.drained() }

func (s roomSink) Fail() { s.c.closeWith(CodeTryAgainLater) }

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

func (c *connection) loadRooms() {
	if m := c.currentMember(); m != nil {
		m.LoadRooms()
	}
}

func (c *connection) closeRoom(room string) {
	if m := c.currentMember(); m != nil {
		m.Close(room)
	}
}

func (c *connection) typing(f *chatimv1.TypingFrame) {
	if !c.typist.Allow() {
		c.server.typing.RateLimited()
		return
	}
	claims := c.currentClaims()
	c.server.typing.FromClient(c.currentMember(), claims.Tenant, claims.User, f)
}
