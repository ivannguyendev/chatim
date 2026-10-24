package wstest

import (
	"bufio"
	"net"
	"net/http"
	"testing"

	"github.com/lxzan/gws"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type received struct {
	opcode gws.Opcode
	frame  *chatimv1.ClientFrame
	raw    []byte
}

type fakeServer struct {
	gws.BuiltinEventHandler
	got     chan received
	onFrame func(conn *gws.Conn, frame *chatimv1.ClientFrame)
}

func newFakeServer(onFrame func(conn *gws.Conn, frame *chatimv1.ClientFrame)) *fakeServer {
	return &fakeServer{got: make(chan received, 4096), onFrame: onFrame}
}

func (s *fakeServer) OnMessage(conn *gws.Conn, message *gws.Message) {
	defer func() { _ = message.Close() }()
	r := received{opcode: message.Opcode, raw: append([]byte(nil), message.Bytes()...)}
	frame := &chatimv1.ClientFrame{}
	if message.Opcode == gws.OpcodeBinary && proto.Unmarshal(r.raw, frame) == nil {
		r.frame = frame
	}
	s.got <- r
	if s.onFrame != nil && r.frame != nil {
		s.onFrame(conn, r.frame)
	}
}

func (s *fakeServer) drain() []received {
	var out []received
	for {
		select {
		case r := <-s.got:
			out = append(out, r)
		default:
			return out
		}
	}
}

func dialPipe(t *testing.T, server *fakeServer) *Conn {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	upgrader := gws.NewUpgrader(server, &gws.ServerOption{SubProtocols: []string{Subprotocol}})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		reader := bufio.NewReader(serverSide)
		req, err := http.ReadRequest(reader)
		if err != nil {
			_ = serverSide.Close()
			return
		}
		conn, err := upgrader.UpgradeFromConn(serverSide, reader, req)
		if err != nil {
			return
		}
		conn.ReadLoop()
	}()
	t.Cleanup(func() { <-serverDone })
	c, err := dialConn(t.Context(), clientSide, "ws://chatim.test/ws")
	if err != nil {
		t.Fatalf("dialConn: %v", err)
	}
	return c
}

func push(t *testing.T, conn *gws.Conn, frame *chatimv1.ServerFrame) {
	t.Helper()
	payload, err := proto.Marshal(frame)
	if err != nil {
		t.Errorf("marshal: %v", err)
		return
	}
	_ = conn.WriteMessage(gws.OpcodeBinary, payload)
}

func respond(t *testing.T, conn *gws.Conn, resp *chatimv1.ServerResponse) {
	t.Helper()
	push(t, conn, &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Response{Response: resp}})
}

func ready() *chatimv1.ServerFrame {
	return &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Ready{Ready: &chatimv1.ReadyFrame{}}}
}

func eventFrame(seq uint64) *chatimv1.ServerFrame {
	return &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Event{Event: &chatimv1.Event{RoomId: "7", Seq: seq}}}
}
