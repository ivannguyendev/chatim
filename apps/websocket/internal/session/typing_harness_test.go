package session

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/typing"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func newTestTyping(t *testing.T, viewers typing.Viewers) *typing.Service {
	t.Helper()
	svc, err := typing.New(typing.Config{Origin: "ws-test:00000000", Queue: 16}, viewers)
	if err != nil {
		t.Fatalf("typing.New: %v", err)
	}
	return svc
}

func (tc *testClient) typing(room string, stopped bool) {
	tc.frame(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Typing{Typing: &chatimv1.TypingFrame{RoomId: room, Stopped: stopped}}})
}

func (h *harness) typingCount(name, reason string) float64 {
	h.t.Helper()
	for _, s := range h.typing.Sources() {
		if s.Name == name && (reason == "" || s.Labels["reason"] == reason) {
			return s.Read()
		}
	}
	h.t.Fatalf("no metric %s %s", name, reason)
	return 0
}

func (h *harness) readyClient(user string, open ...string) *testClient {
	h.t.Helper()
	tc := h.dial(true)
	tc.auth(h.token(user, time.Hour))
	if f := tc.nextFrame(); f.GetReady() == nil {
		h.t.Fatalf("first frame of %s = %v, want ready", user, f)
	}
	for _, room := range open {
		tc.openRoom(room)
	}
	return tc
}
