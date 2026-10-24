package wstest

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lxzan/gws"
	"go.uber.org/goleak"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func sendRequest(room string) *chatimv1.ClientRequest {
	return &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_SendMessage{SendMessage: &chatimv1.SendMessageRequest{RoomId: room}}}
}

func TestParallelCallsGetTheirOwnResponseAndIncreasingIDs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const calls = 30
		var requests []*chatimv1.ClientRequest
		server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
			if req := frame.GetRequest(); req != nil {
				requests = append(requests, req)
			}
			if len(requests) < calls {
				return
			}
			for _, r := range slices.Backward(requests) {
				seq, _ := strconv.ParseUint(r.GetSendMessage().GetRoomId(), 10, 64)
				respond(t, conn, &chatimv1.ServerResponse{Id: r.GetId(), Result: &chatimv1.ServerResponse_SendMessage{SendMessage: &chatimv1.SendMessageResponse{Seq: seq}}})
			}
		})
		c := dialPipe(t, server)
		var wg sync.WaitGroup
		for i := range calls {
			wg.Go(func() {
				resp, err := c.Call(t.Context(), sendRequest(strconv.Itoa(i)))
				if err != nil || resp.GetSendMessage().GetSeq() != uint64(i) {
					t.Errorf("call %d = %v, %v", i, resp, err)
				}
			})
		}
		wg.Wait()
		ids := make([]uint64, 0, calls)
		for _, r := range requests {
			ids = append(ids, r.GetId())
		}
		slices.Sort(ids)
		for i, id := range ids {
			if id != uint64(i+1) {
				t.Fatalf("request ids %v, want 1..%d", ids, calls)
			}
		}
		_ = c.Close()
	})
}

func TestCallMapsTheResponseCodeToAStatusError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
			if req := frame.GetRequest(); req != nil {
				respond(t, conn, &chatimv1.ServerResponse{Id: req.GetId(), Code: int32(codes.Unimplemented), Message: "request not supported"})
			}
		})
		c := dialPipe(t, server)
		defer func() { _ = c.Close() }()
		resp, err := c.Call(t.Context(), sendRequest("7"))
		if status.Code(err) != codes.Unimplemented || resp.GetMessage() != "request not supported" {
			t.Fatalf("call = %v, %v, want Unimplemented with the response", resp, err)
		}
	})
}

func TestAuthThenHeartbeatsEveryFiveSecondsUntilClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(nil)
		c := dialPipe(t, server)
		if err := c.Auth("token-1"); err != nil {
			t.Fatalf("auth: %v", err)
		}
		time.Sleep(time.Minute + time.Second)
		synctest.Wait()
		got := server.drain()
		if len(got) != 13 || got[0].frame.GetAuth().GetToken() != "token-1" {
			t.Fatalf("frames = %d, first %v; want auth then 12 heartbeats", len(got), got[0].frame)
		}
		for _, r := range got[1:] {
			if r.frame.GetHeartbeat() == nil {
				t.Fatalf("frame %v, want a heartbeat", r.frame)
			}
		}
		if err := c.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		for _, r := range server.drain() {
			if r.frame.GetHeartbeat() != nil {
				t.Fatal("a heartbeat was sent after Close")
			}
		}
	})
}

func TestFramesKeepsEveryNonResponseFrameInOrderWithoutAReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const pushed = 5000
		server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
			if frame.GetLoadRooms() == nil {
				return
			}
			push(t, conn, ready())
			respond(t, conn, &chatimv1.ServerResponse{Id: 99})
			for i := range pushed {
				push(t, conn, eventFrame(uint64(i+1)))
			}
			_ = conn.WriteClose(CodeUnauthenticated, nil)
		})
		c := dialPipe(t, server)
		if err := c.LoadRooms(); err != nil {
			t.Fatalf("load rooms: %v", err)
		}
		<-c.Done()
		if closeErr, ok := errors.AsType[*CloseError](c.Err()); !ok || closeErr.Code != CodeUnauthenticated {
			t.Fatalf("Err = %v, want close code %d", c.Err(), CodeUnauthenticated)
		}
		first := <-c.Frames()
		if first.GetReady() == nil {
			t.Fatalf("first frame %v, want ready", first)
		}
		next := uint64(1)
		for f := range c.Frames() {
			if f.GetEvent().GetSeq() != next {
				t.Fatalf("frame %v, want event seq %d", f, next)
			}
			next++
		}
		if next != pushed+1 {
			t.Fatalf("got %d events, want %d", next-1, pushed)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("close after the server closed: %v", err)
		}
	})
}

func TestCallReturnsTheCloseErrorWhenTheServerCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
			if frame.GetRequest() != nil {
				_ = conn.WriteClose(CodeUnauthenticated, nil)
			}
		})
		c := dialPipe(t, server)
		defer func() { _ = c.Close() }()
		_, err := c.Call(t.Context(), sendRequest("7"))
		if closeErr, ok := errors.AsType[*CloseError](err); !ok || closeErr.Code != CodeUnauthenticated {
			t.Fatalf("call = %v, want close code %d", err, CodeUnauthenticated)
		}
		if _, err := c.Call(t.Context(), sendRequest("7")); !errors.Is(err, ErrClosed) {
			t.Fatalf("call after close = %v, want the close error", err)
		}
	})
}

func TestSendersWriteOneFrameEach(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(nil)
		c := dialPipe(t, server)
		steps := []func() error{
			func() error { return c.OpenRoom("7") },
			func() error { return c.CloseRoom("7") },
			func() error { return c.LoadRooms() },
			func() error { return c.Typing("7", false) },
			func() error { return c.Typing("7", true) },
			func() error { return c.WatchPresence([]string{"a", "b"}) },
			func() error {
				return c.Send(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Heartbeat{Heartbeat: &chatimv1.HeartbeatFrame{}}})
			},
			func() error { return c.SendRaw(gws.OpcodeText, []byte("{}")) },
		}
		for i, step := range steps {
			if err := step(); err != nil {
				t.Fatalf("sender %d: %v", i, err)
			}
		}
		synctest.Wait()
		got := server.drain()
		if len(got) != len(steps) {
			t.Fatalf("server got %d frames, want %d", len(got), len(steps))
		}
		checks := []bool{
			got[0].frame.GetOpenRoom().GetRoomId() == "7",
			got[1].frame.GetCloseRoom().GetRoomId() == "7",
			got[2].frame.GetLoadRooms() != nil,
			got[3].frame.GetTyping().GetRoomId() == "7" && !got[3].frame.GetTyping().GetStopped(),
			got[4].frame.GetTyping().GetStopped(),
			slices.Equal(got[5].frame.GetWatchPresence().GetUserIds(), []string{"a", "b"}),
			got[6].frame.GetHeartbeat() != nil,
			got[7].opcode == gws.OpcodeText && string(got[7].raw) == "{}",
		}
		for i, ok := range checks {
			if !ok {
				t.Fatalf("frame %d = %v (%q), not what sender %d writes", i, got[i].frame, got[i].raw, i)
			}
		}
		if err := c.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("second close: %v", err)
		}
		if err := c.OpenRoom("7"); err == nil {
			t.Fatal("a send after Close succeeded")
		}
		if _, ok := <-c.Frames(); ok {
			t.Fatal("Frames stayed open after Close")
		}
	})
}
