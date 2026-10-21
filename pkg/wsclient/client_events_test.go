package wsclient

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lxzan/gws"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type handlerLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *handlerLog) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *handlerLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines)
}

func (l *handlerLog) options() []Option {
	return []Option{
		OnEvent(func(ev *chatimv1.Event) { l.add("event %s", ev.GetId()) }),
		OnNotification(func(n *chatimv1.Notification) { l.add("notification %s", n.GetId()) }),
		OnReady(func() { l.add("ready") }),
		OnResync(func(reason chatimv1.ResyncReason) { l.add("resync %s", reason) }),
		OnRoomClosed(func(room string, reason chatimv1.RoomClosedReason) { l.add("room_closed %s %s", room, reason) }),
	}
}

func eventFrame(ev *chatimv1.Event) *chatimv1.ServerFrame {
	return &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Event{Event: ev}}
}

func TestServerFramesReachTheirHandlersInOrderAfterTheApplier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
			if frame.GetAuth() == nil {
				return
			}
			for _, f := range []*chatimv1.ServerFrame{
				{Frame: &chatimv1.ServerFrame_Ready{Ready: &chatimv1.ReadyFrame{}}},
				eventFrame(created("1", 0, 1)),
				eventFrame(created("1", 0, 1)),
				{Frame: &chatimv1.ServerFrame_Notification{Notification: &chatimv1.Notification{Id: "n1"}}},
				{Frame: &chatimv1.ServerFrame_Resync{Resync: &chatimv1.ResyncFrame{Reason: chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD}}},
				{},
				{Frame: &chatimv1.ServerFrame_RoomClosed{RoomClosed: &chatimv1.RoomClosedFrame{
					RoomId: "1", Reason: chatimv1.RoomClosedReason_ROOM_CLOSED_REASON_REMOVED,
				}}},
				eventFrame(created("1", 0, 2)),
			} {
				writeServerFrame(t, conn, f)
			}
			_ = conn.WriteClose(1000, nil)
		})
		var log handlerLog
		applier := NewApplier(0)
		client := dialPipe(t, server, "token", append(log.options(), WithApplier(applier))...)
		<-client.Done()
		_ = client.Close()
		want := []string{
			"ready",
			"event 1-0-1",
			"notification n1",
			"resync RESYNC_REASON_LAG_TOO_OLD",
			"room_closed 1 ROOM_CLOSED_REASON_REMOVED",
			"event 1-0-2",
		}
		if got := log.snapshot(); !slices.Equal(got, want) {
			t.Fatalf("handler calls %q, want %q", got, want)
		}
		if stats := applier.Stats(); stats.Duplicates != 1 || stats.Delivered != 2 {
			t.Fatalf("applier stats %+v, want 2 delivered and 1 duplicate", stats)
		}
	})
}

func TestOpenAndCloseRoomSendTheirFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(nil)
		client := dialPipe(t, server, "token")
		if err := client.OpenRoom(t.Context(), "7"); err != nil {
			t.Fatalf("OpenRoom: %v", err)
		}
		if err := client.CloseRoom(t.Context(), "7"); err != nil {
			t.Fatalf("CloseRoom: %v", err)
		}
		synctest.Wait()
		frames := server.drain()
		if len(frames) != 3 || frames[1].frame.GetOpenRoom().GetRoomId() != "7" || frames[2].frame.GetCloseRoom().GetRoomId() != "7" {
			t.Fatalf("frames %v, want auth, open_room 7, close_room 7", frames)
		}
		_ = client.Close()
	})
}

func TestGapHandlerFetchesTheMissingRangeBeforeTheEventIsDelivered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var history []*chatimv1.GetHistoryRequest
		server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
			if frame.GetAuth() != nil {
				writeServerFrame(t, conn, eventFrame(created("1", 0, 1)))
				writeServerFrame(t, conn, eventFrame(created("1", 0, 4)))
				writeServerFrame(t, conn, eventFrame(created("1", 0, 4)))
			}
			if req := frame.GetRequest(); req != nil {
				history = append(history, req.GetGetHistory())
				respond(t, conn, &chatimv1.ServerResponse{Id: req.GetId(), Result: &chatimv1.ServerResponse_GetHistory{
					GetHistory: &chatimv1.GetHistoryResponse{},
				}})
			}
		})
		var log handlerLog
		fill := OnGap(func(c *Client, gap Gap) {
			_, err := c.Call(t.Context(), &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_GetHistory{
				GetHistory: &chatimv1.GetHistoryRequest{
					RoomId: gap.RoomID, ThreadRoot: gap.ThreadRoot, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_AFTER,
					Seq: gap.From - 1, Limit: int32(gap.To - gap.From + 1),
				},
			}})
			log.add("gap %s/%d %d..%d err=%v", gap.RoomID, gap.ThreadRoot, gap.From, gap.To, err)
		})
		client := dialPipe(t, server, "token", append(log.options(), fill)...)
		synctest.Wait()
		_ = client.Close()
		want := []string{"event 1-0-1", "gap 1/0 2..3 err=<nil>", "event 1-0-4"}
		if got := log.snapshot(); !slices.Equal(got, want) {
			t.Fatalf("handler calls %q, want %q", got, want)
		}
		if len(history) != 1 || history[0].GetSeq() != 1 || history[0].GetLimit() != 2 {
			t.Fatalf("history requests %v, want one after seq 1 with limit 2", history)
		}
	})
}

func TestSlowHandlerKeepsCallsAndHeartbeatsFlowingAndOverflowAsksForResync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
			switch {
			case frame.GetAuth() != nil:
				writeServerFrame(t, conn, &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Ready{Ready: &chatimv1.ReadyFrame{}}})
			case frame.GetOpenRoom() != nil:
				for seq := uint64(1); seq <= 5; seq++ {
					writeServerFrame(t, conn, eventFrame(created("1", 0, seq)))
				}
			case frame.GetRequest() != nil:
				respond(t, conn, &chatimv1.ServerResponse{Id: frame.GetRequest().GetId()})
			}
		})
		var log handlerLog
		release := make(chan struct{})
		blockReady := OnReady(func() {
			<-release
			log.add("ready")
		})
		client := dialPipe(t, server, "token", append(log.options(), blockReady, WithDispatchQueue(2))...)
		synctest.Wait()
		if err := client.OpenRoom(t.Context(), "1"); err != nil {
			t.Fatalf("OpenRoom: %v", err)
		}
		synctest.Wait()
		if _, err := client.Call(t.Context(), sendMessageRequest("1")); err != nil {
			t.Fatalf("call while a handler blocks: %v", err)
		}
		server.drain()
		time.Sleep(30 * time.Second)
		synctest.Wait()
		heartbeats := 0
		for _, f := range server.drain() {
			if f.frame.GetHeartbeat() != nil {
				heartbeats++
			}
		}
		if heartbeats < int(30*time.Second/HeartbeatMax) {
			t.Fatalf("%d heartbeats in 30s while a handler blocks, want at least %d", heartbeats, int(30*time.Second/HeartbeatMax))
		}
		close(release)
		synctest.Wait()
		got := log.snapshot()
		slices.Sort(got)
		want := []string{"event 1-0-1", "event 1-0-2", "ready", "resync RESYNC_REASON_CLIENT_BACKLOG"}
		if !slices.Equal(got, want) {
			t.Fatalf("handler calls %q, want %q in any order", got, want)
		}
		_ = client.Close()
	})
}
