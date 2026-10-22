package wsclient

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/lxzan/gws"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func loadFailedOnAuth(t *testing.T, retryAfterMs uint32) *fakeServer {
	return newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
		if frame.GetAuth() != nil {
			writeServerFrame(t, conn, &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_LoadFailed{
				LoadFailed: &chatimv1.LoadFailedFrame{RetryAfterMs: retryAfterMs},
			}})
		}
	})
}

func loadRoomsFrames(frames []receivedFrame) []time.Time {
	var at []time.Time
	for _, f := range frames {
		if f.frame.GetLoadRooms() != nil {
			at = append(at, f.at)
		}
	}
	return at
}

func TestWithoutAHandlerLoadFailedSendsLoadRoomsAfterTheHintPlusJitter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := loadFailedOnAuth(t, 2000)
		start := time.Now()
		client := dialPipe(t, server, "token")
		synctest.Wait()
		time.Sleep(2500*time.Millisecond - time.Nanosecond)
		synctest.Wait()
		if got := loadRoomsFrames(server.drain()); len(got) != 0 {
			t.Fatalf("load_rooms sent before the hint plus the smallest jitter: %v", got)
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		got := loadRoomsFrames(server.drain())
		if len(got) != 1 || got[0].Sub(start) < 2500*time.Millisecond || got[0].Sub(start) > 3000*time.Millisecond {
			t.Fatalf("load_rooms at %v, want once between 2.5s and 3s", got)
		}
		_ = client.Close()
	})
}

func TestALoadFailedHandlerDecidesAndNothingIsSentForIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := loadFailedOnAuth(t, 1500)
		hints := make(chan time.Duration, 1)
		client := dialPipe(t, server, "token", OnLoadFailed(func(c *Client, retryAfter time.Duration) {
			if c == nil {
				t.Error("handler got no client")
			}
			hints <- retryAfter
		}))
		synctest.Wait()
		if got := <-hints; got != 1500*time.Millisecond {
			t.Fatalf("hint = %v", got)
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if got := loadRoomsFrames(server.drain()); len(got) != 0 {
			t.Fatalf("load_rooms sent although the app handles load_failed: %v", got)
		}
		if err := client.LoadRooms(t.Context()); err != nil {
			t.Fatalf("LoadRooms: %v", err)
		}
		synctest.Wait()
		if got := loadRoomsFrames(server.drain()); len(got) != 1 {
			t.Fatalf("explicit LoadRooms sent %d frames", len(got))
		}
		_ = client.Close()
	})
}
