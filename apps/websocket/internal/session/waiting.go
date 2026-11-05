package session

import (
	"time"

	"github.com/lxzan/gws"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *connection) startGrace() {
	stopTimer(c.grace)
	if c.finished {
		return
	}
	gen := c.authGen
	c.grace = time.AfterFunc(c.server.opts.AuthGrace, func() { c.graceExpired(gen) })
}

func (c *connection) graceExpired(gen uint64) {
	defer c.recoverPanic()
	c.mu.Lock()
	waiting := !c.finished && !c.running && c.authGen == gen
	c.mu.Unlock()
	if waiting {
		c.closeWith(CodeUnauthenticated)
	}
}

func (c *connection) tokenExpired(gen uint64) {
	defer c.recoverPanic()
	if !c.stopRunning(gen) {
		return
	}
	c.server.stats.authRequiredExpired.Add(1)
	c.sendAuthRequired(promptExpired)
}

func (c *connection) stopRunning(gen uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished || !c.running || c.authGen != gen {
		return false
	}
	if remaining := time.Until(c.deadlineOf(c.claims)); remaining > 0 {
		c.armExpiry(remaining)
		return false
	}
	c.toWaiting(promptExpired)
	return true
}

func (c *connection) toWaiting(reason chatimv1.AuthRequiredReason) {
	stopTimer(c.expiry)
	c.running = false
	c.authGen++
	c.reason = reason
	if c.member != nil {
		c.member.Unregister()
	}
	if c.watch != nil {
		c.watch.drop()
	}
	c.member, c.watch = nil, nil
	if c.server.stats.unauth.Add(1) > int64(c.server.opts.UnauthMax) {
		c.server.stats.unauthOverCap.Add(1)
	}
	c.startGrace()
}

func (c *connection) setReason(reason chatimv1.AuthRequiredReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reason = reason
}

func (c *connection) promptAuth() {
	if !c.prompts.Allow() {
		return
	}
	c.mu.Lock()
	reason := c.reason
	c.mu.Unlock()
	c.sendAuthRequired(reason)
}

func (c *connection) sendAuthRequired(reason chatimv1.AuthRequiredReason) {
	c.sendFrame(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_AuthRequired{AuthRequired: &chatimv1.AuthRequiredFrame{Reason: reason}}}, sendControl)
}

func (c *connection) sendFrame(frame *chatimv1.ServerFrame, kind sendKind) {
	payload, err := proto.Marshal(frame)
	if err != nil {
		c.server.logger.Error("encode websocket frame", "error", err)
		c.closeWith(CodeInternalError)
		return
	}
	c.enqueue(outFrame{opcode: gws.OpcodeBinary, payload: payload}, kind)
}
