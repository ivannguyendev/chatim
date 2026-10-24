package session

import (
	"time"

	"github.com/lxzan/gws"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type roomSink struct {
	c *connection
}

func (s roomSink) Send(frame []byte) { s.c.enqueue(outFrame{opcode: gws.OpcodeBinary, payload: frame}) }

func (s roomSink) TrySend(frame []byte) bool {
	return s.c.tryEnqueue(outFrame{opcode: gws.OpcodeBinary, payload: frame})
}

func (s roomSink) Offer(frame []byte) bool {
	return s.c.offer(outFrame{opcode: gws.OpcodeBinary, payload: frame})
}

func (s roomSink) Fail() { s.c.closeWith(CodeTryAgainLater) }

func (c *connection) join(claims jwtverify.Claims) {
	c.server.presence.Touch(claims.Tenant, claims.User, time.Now())
	member := c.server.interest.Register(claims.Tenant, claims.User, roomSink{c: c})
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		member.Unregister()
		return
	}
	c.member = member
	c.watch = newPresenceWatch(&c.server.presenceStats)
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
