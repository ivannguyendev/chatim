package wsclient

import (
	"crypto/rand"
	"math/big"
	"time"

	"github.com/lxzan/gws"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	HeartbeatMin = 5 * time.Second
	HeartbeatMax = 8 * time.Second

	writeTimeout         = 10 * time.Second
	closeUnsupportedData = 1003
	closePolicyViolation = 1008
)

var heartbeatFrame, _ = proto.Marshal(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Heartbeat{
	Heartbeat: &chatimv1.HeartbeatFrame{},
}})

func heartbeatInterval() time.Duration {
	jitter, err := rand.Int(rand.Reader, big.NewInt(int64(HeartbeatMax-HeartbeatMin)))
	if err != nil {
		return HeartbeatMin
	}
	return HeartbeatMin + time.Duration(jitter.Int64())
}

func (c *Client) writeLoop() {
	heartbeat := time.NewTimer(heartbeatInterval())
	defer heartbeat.Stop()
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.out:
			c.write(frame)
		case <-heartbeat.C:
			c.write(heartbeatFrame)
			heartbeat.Reset(heartbeatInterval())
		}
	}
}

func (c *Client) write(frame []byte) {
	_ = c.conn.NetConn().SetWriteDeadline(time.Now().Add(writeTimeout))
	_ = c.conn.WriteMessage(gws.OpcodeBinary, frame)
}

type events struct {
	gws.BuiltinEventHandler
	client *Client
}

func (e events) OnMessage(conn *gws.Conn, message *gws.Message) {
	defer func() { _ = message.Close() }()
	if message.Opcode != gws.OpcodeBinary {
		_ = conn.WriteClose(closeUnsupportedData, nil)
		return
	}
	frame := &chatimv1.ServerFrame{}
	if err := proto.Unmarshal(message.Bytes(), frame); err != nil {
		_ = conn.WriteClose(closePolicyViolation, nil)
		return
	}
	if resp := frame.GetResponse(); resp != nil {
		e.client.deliver(resp)
		return
	}
	if dispatched(frame) {
		e.client.enqueue(frame)
	}
}

func (e events) OnClose(_ *gws.Conn, err error) {
	e.client.finish(closeErrorOf(err))
}
