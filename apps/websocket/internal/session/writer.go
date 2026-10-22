package session

import (
	"time"

	"github.com/lxzan/gws"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type outFrame struct {
	opcode  gws.Opcode
	payload []byte
}

type admission int

const (
	admitted admission = iota
	admitDropped
	admitOverBudget
	admitQueueFull
)

func (c *connection) writeLoop() {
	defer c.recoverWriter()
	for {
		select {
		case code := <-c.closeReq:
			c.writeClose(code)
			return
		case <-c.closed:
			return
		default:
		}
		select {
		case code := <-c.closeReq:
			c.writeClose(code)
			return
		case frame := <-c.out:
			written := c.write(frame)
			c.written(frame)
			if !written {
				c.abort()
				return
			}
		case <-c.closed:
			return
		}
	}
}

func (c *connection) write(frame outFrame) bool {
	if err := c.socket.SetWriteDeadline(time.Now().Add(c.server.opts.WriteTimeout)); err != nil {
		return false
	}
	return c.socket.WriteMessage(frame.opcode, frame.payload) == nil
}

func (c *connection) writeClose(code uint16) {
	if err := c.socket.SetWriteDeadline(time.Now().Add(c.server.opts.WriteTimeout)); err != nil {
		c.abort()
		return
	}
	for {
		select {
		case frame := <-c.out:
			err := c.socket.WriteMessage(frame.opcode, frame.payload)
			c.written(frame)
			if err != nil {
				c.abort()
				return
			}
		default:
			_ = c.socket.WriteClose(code, nil)
			return
		}
	}
}

func (c *connection) respond(resp *chatimv1.ServerResponse) {
	payload, err := proto.Marshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Response{Response: resp}})
	if err != nil {
		c.server.logger.Error("encode websocket response", "error", err)
		c.closeWith(CodeInternalError)
		return
	}
	c.enqueue(outFrame{opcode: gws.OpcodeBinary, payload: payload})
}

func (c *connection) enqueue(frame outFrame) {
	switch c.admit(frame) {
	case admitOverBudget:
		c.closeWith(CodeSlowClient)
	case admitQueueFull:
		c.abort()
	case admitted, admitDropped:
	}
}

func (c *connection) tryEnqueue(frame outFrame) bool { return c.admit(frame) == admitted }

func (c *connection) admit(frame outFrame) admission {
	select {
	case <-c.closed:
		return admitDropped
	default:
	}
	size := int64(len(frame.payload))
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	switch {
	case c.sealed:
		return admitDropped
	case c.queuedBytes+size > int64(c.server.opts.SendQueueBytes):
		return admitOverBudget
	}
	select {
	case c.out <- frame:
		c.queuedBytes += size
		c.server.stats.queuedBytes.Add(size)
		return admitted
	default:
		return admitQueueFull
	}
}

func (c *connection) written(frame outFrame) {
	size := int64(len(frame.payload))
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	if c.sealed {
		return
	}
	c.queuedBytes -= size
	c.server.stats.queuedBytes.Add(-size)
}

func (c *connection) sealQueue() {
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	c.sealed = true
	c.server.stats.queuedBytes.Add(-c.queuedBytes)
	c.queuedBytes = 0
}

func (c *connection) closeWith(code uint16) {
	if c.closing.CompareAndSwap(0, uint32(code)) {
		c.closeReq <- code
	}
}

func (c *connection) abort() {
	c.aborted.Store(true)
	_ = c.socket.NetConn().Close()
}
