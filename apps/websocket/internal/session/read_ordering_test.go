package session

import (
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (tc *testClient) markRead(id uint64, room string, seq uint64) {
	tc.request(&chatimv1.ClientRequest{Id: id, Request: &chatimv1.ClientRequest_MarkRead{MarkRead: &chatimv1.MarkReadRequest{RoomId: room, Seq: seq}}})
}

func (tc *testClient) markUnread(id uint64, room string, seq uint64) {
	tc.request(&chatimv1.ClientRequest{Id: id, Request: &chatimv1.ClientRequest_MarkUnread{MarkUnread: &chatimv1.MarkUnreadRequest{RoomId: room, Seq: seq}}})
}

func TestReadMovesOfOneRoomKeepOrderWithSendsAndOtherRoomsRunInParallel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		read, send, unread, other := h.core.hold(1), h.core.hold(2), h.core.hold(3), h.core.hold(4)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.markRead(1, "77", 9)
		tc.send(2, "77")
		tc.markUnread(3, "77", 4)
		tc.markRead(4, "88", 1)
		synctest.Wait()
		if got := h.core.startedIDs(); !sameIDs(got, 1, 4) {
			t.Fatalf("started = %v, want the first read of room 77 and the read of room 88", got)
		}
		for i, step := range []struct {
			release chan codes.Code
			id      uint64
			next    []uint64
		}{{read, 1, []uint64{2}}, {send, 2, []uint64{3}}, {unread, 3, nil}} {
			step.release <- codes.OK
			if resp := tc.next(); resp.GetId() != step.id {
				t.Fatalf("step %d: response id = %d, want %d", i, resp.GetId(), step.id)
			}
			synctest.Wait()
			if got := h.core.startedIDs(); !sameIDs(got, step.next...) {
				t.Fatalf("step %d: started = %v, want %v", i, got, step.next)
			}
		}
		other <- codes.OK
		tc.next()
		tc.close()
	})
}
