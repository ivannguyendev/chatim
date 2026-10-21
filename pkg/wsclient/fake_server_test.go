package wsclient

import (
	"bufio"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/lxzan/gws"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type receivedFrame struct {
	at    time.Time
	frame *chatimv1.ClientFrame
}

type fakeServer struct {
	gws.BuiltinEventHandler
	frames  chan receivedFrame
	onFrame func(conn *gws.Conn, frame *chatimv1.ClientFrame)
}

func newFakeServer(onFrame func(conn *gws.Conn, frame *chatimv1.ClientFrame)) *fakeServer {
	return &fakeServer{frames: make(chan receivedFrame, 1024), onFrame: onFrame}
}

func (s *fakeServer) OnMessage(conn *gws.Conn, message *gws.Message) {
	defer func() { _ = message.Close() }()
	frame := &chatimv1.ClientFrame{}
	if err := proto.Unmarshal(message.Bytes(), frame); err != nil {
		_ = conn.WriteClose(closePolicyViolation, nil)
		return
	}
	s.frames <- receivedFrame{at: time.Now(), frame: frame}
	if s.onFrame != nil {
		s.onFrame(conn, frame)
	}
}

func (s *fakeServer) drain() []receivedFrame {
	var got []receivedFrame
	for {
		select {
		case f := <-s.frames:
			got = append(got, f)
		default:
			return got
		}
	}
}

func dialPipe(t *testing.T, server *fakeServer, token string, opts ...Option) *Client {
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
	client, err := DialConn(t.Context(), clientSide, "ws://chatim.test/ws", token, opts...)
	if err != nil {
		t.Fatalf("DialConn: %v", err)
	}
	return client
}

func writeServerFrame(t *testing.T, conn *gws.Conn, frame *chatimv1.ServerFrame) {
	t.Helper()
	payload, err := proto.Marshal(frame)
	if err != nil {
		t.Errorf("marshal server frame: %v", err)
		return
	}
	_ = conn.WriteMessage(gws.OpcodeBinary, payload)
}

func respond(t *testing.T, conn *gws.Conn, resp *chatimv1.ServerResponse) {
	t.Helper()
	writeServerFrame(t, conn, &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Response{Response: resp}})
}

func sendMessageRequest(room string) *chatimv1.ClientRequest {
	return &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_SendMessage{
		SendMessage: &chatimv1.SendMessageRequest{RoomId: room},
	}}
}
