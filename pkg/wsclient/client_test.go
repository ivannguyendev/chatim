package wsclient

import (
	"context"
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

func TestParallelCallsGetTheResponseWithTheirOwnID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const calls = 50
		var requests []*chatimv1.ClientRequest
		server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
			req := frame.GetRequest()
			if req == nil {
				return
			}
			requests = append(requests, req)
			if len(requests) < calls {
				return
			}
			for _, r := range slices.Backward(requests) {
				seq, _ := strconv.ParseUint(r.GetSendMessage().GetRoomId(), 10, 64)
				respond(t, conn, &chatimv1.ServerResponse{Id: r.GetId(), Result: &chatimv1.ServerResponse_SendMessage{
					SendMessage: &chatimv1.SendMessageResponse{Seq: seq},
				}})
			}
		})
		client := dialPipe(t, server, "token")
		var wg sync.WaitGroup
		for i := range calls {
			wg.Go(func() {
				resp, err := client.Call(t.Context(), sendMessageRequest(strconv.Itoa(i)))
				if err != nil {
					t.Errorf("call %d: %v", i, err)
					return
				}
				if got := resp.GetSendMessage().GetSeq(); got != uint64(i) {
					t.Errorf("call %d got the response for %d", i, got)
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
				t.Fatalf("request ids %v, want 1..%d once each", ids, calls)
			}
		}
		_ = client.Close()
	})
}

func TestFirstFrameIsAuthAndHeartbeatsComeEveryFiveToEightSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		server := newFakeServer(nil)
		client := dialPipe(t, server, "token-1")
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		frames := server.drain()
		if len(frames) == 0 || frames[0].frame.GetAuth().GetToken() != "token-1" {
			t.Fatalf("first frame %v, want auth with token-1", frames)
		}
		previous := start
		for _, f := range frames[1:] {
			if f.frame.GetHeartbeat() == nil {
				t.Fatalf("frame %v, want heartbeat", f.frame)
			}
			if gap := f.at.Sub(previous); gap < HeartbeatMin || gap > HeartbeatMax {
				t.Fatalf("heartbeat gap %v outside [%v, %v]", gap, HeartbeatMin, HeartbeatMax)
			}
			previous = f.at
		}
		if n := len(frames) - 1; n < int(2*time.Minute/HeartbeatMax) {
			t.Fatalf("%d heartbeats in 2m, want at least %d", n, int(2*time.Minute/HeartbeatMax))
		}
		_ = client.Close()
	})
}

func TestServerCloseCodesMapToErrors(t *testing.T) {
	cases := []struct {
		code uint16
		want error
	}{
		{code: CodeUnauthenticated, want: ErrUnauthenticated},
		{code: CodeAuthTimeout, want: ErrAuthTimeout},
		{code: 1001, want: ErrClosed},
		{code: 1008, want: ErrClosed},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(int(tc.code)), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
					if frame.GetRequest() != nil {
						_ = conn.WriteClose(tc.code, nil)
					}
				})
				client := dialPipe(t, server, "token")
				_, err := client.Call(t.Context(), sendMessageRequest("1"))
				if !errors.Is(err, tc.want) {
					t.Fatalf("pending call error %v, want %v", err, tc.want)
				}
				closeErr, ok := errors.AsType[*CloseError](client.Err())
				if !ok || closeErr.Code != tc.code {
					t.Fatalf("Err() = %v, want a CloseError with code %d", client.Err(), tc.code)
				}
				if _, err := client.Call(t.Context(), sendMessageRequest("2")); !errors.Is(err, tc.want) {
					t.Fatalf("call after close error %v, want %v", err, tc.want)
				}
				if err := client.Renew("new"); !errors.Is(err, tc.want) {
					t.Fatalf("renew after close error %v, want %v", err, tc.want)
				}
				if err := client.Close(); err != nil {
					t.Fatalf("Close after server close: %v", err)
				}
			})
		})
	}
}

func TestCloseFailsPendingCallsAndStopsEveryGoroutine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := dialPipe(t, newFakeServer(nil), "token")
		result := make(chan error, 1)
		go func() {
			_, err := client.Call(t.Context(), sendMessageRequest("1"))
			result <- err
		}()
		synctest.Wait()
		if err := client.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := <-result; !errors.Is(err, ErrClosed) {
			t.Fatalf("pending call error %v, want ErrClosed", err)
		}
		if err := client.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
	})
}

func TestCanceledCallForgetsItsPendingEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := dialPipe(t, newFakeServer(nil), "token")
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err := client.Call(ctx, sendMessageRequest("1")); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("call error %v, want DeadlineExceeded", err)
		}
		client.mu.Lock()
		left := len(client.pending)
		client.mu.Unlock()
		if left != 0 {
			t.Fatalf("%d pending entries left after cancel", left)
		}
		_ = client.Close()
	})
}

func TestUnknownServerFramesAreIgnoredAndErrorCodesBecomeStatusErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
			req := frame.GetRequest()
			if req == nil {
				return
			}
			writeServerFrame(t, conn, &chatimv1.ServerFrame{})
			respond(t, conn, &chatimv1.ServerResponse{Id: req.GetId() + 100})
			respond(t, conn, &chatimv1.ServerResponse{Id: req.GetId(), Code: int32(codes.NotFound), Message: "room not found"})
		})
		client := dialPipe(t, server, "token")
		resp, err := client.Call(t.Context(), sendMessageRequest("1"))
		if status.Code(err) != codes.NotFound || resp.GetMessage() != "room not found" {
			t.Fatalf("call = %v, %v; want NOT_FOUND with the response", resp, err)
		}
		_ = client.Close()
	})
}

func TestRenewSendsANewAuthFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(nil)
		client := dialPipe(t, server, "token-1")
		if err := client.Renew("token-2"); err != nil {
			t.Fatalf("Renew: %v", err)
		}
		synctest.Wait()
		frames := server.drain()
		if len(frames) != 2 || frames[1].frame.GetAuth().GetToken() != "token-2" {
			t.Fatalf("frames %v, want auth token-1 then auth token-2", frames)
		}
		_ = client.Close()
	})
}
