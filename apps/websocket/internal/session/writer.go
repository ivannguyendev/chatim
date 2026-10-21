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
			if !c.write(frame) {
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
			if c.socket.WriteMessage(frame.opcode, frame.payload) != nil {
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
	select {
	case <-c.closed:
		return
	default:
	}
	select {
	case c.out <- frame:
	default:
		c.abort()
	}
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
