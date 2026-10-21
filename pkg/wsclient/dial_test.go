package wsclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lxzan/gws"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func startHTTPServer(t *testing.T, subprotocols []string, server *fakeServer) string {
	t.Helper()
	upgrader := gws.NewUpgrader(server, &gws.ServerOption{SubProtocols: subprotocols})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r)
		if err != nil {
			return
		}
		conn.ReadLoop()
	}))
	t.Cleanup(httpServer.Close)
	return "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
}

func TestDialOverTCPNegotiatesTheSubprotocolAndCalls(t *testing.T) {
	server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
		if req := frame.GetRequest(); req != nil {
			respond(t, conn, &chatimv1.ServerResponse{Id: req.GetId(), Result: &chatimv1.ServerResponse_SendMessage{
				SendMessage: &chatimv1.SendMessageResponse{Seq: 7},
			}})
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := Dial(ctx, startHTTPServer(t, []string{Subprotocol}, server), "token")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	resp, err := client.Call(ctx, sendMessageRequest("1"))
	if err != nil || resp.GetSendMessage().GetSeq() != 7 {
		t.Fatalf("call = %v, %v; want seq 7", resp, err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := (<-server.frames).frame.GetAuth().GetToken(); got != "token" {
		t.Fatalf("first frame token %q, want token", got)
	}
}

func TestDialFailsWhenTheServerRefusesTheSubprotocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := Dial(ctx, startHTTPServer(t, nil, newFakeServer(nil)), "token"); err == nil {
		t.Fatal("Dial succeeded without the chatim.v1 subprotocol")
	}
}
