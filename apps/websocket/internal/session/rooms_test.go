package session

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestAuthLoadsTheRoomsOnceAndSendsReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Minute))
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("first frame after auth = %v, want ready", f)
		}
		tc.auth(h.verifier.issue("renewed", "t1", "u1", time.Now().Add(time.Hour)))
		synctest.Wait()
		tc.noFrame()
		if got := h.loader.calls.Load(); got != 1 {
			t.Fatalf("room loads = %d, want 1 across a token renewal", got)
		}
		tc.close()
		if got := h.interestUsers(); got != 0 {
			t.Fatalf("interest users after close = %v", got)
		}
	})
}

func TestRoomFramesWhileWaitingGetAuthRequired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.openRoom("7")
		tc.expectAuthRequired(promptMissing)
		if h.loader.calls.Load() != 0 || h.interestUsers() != 0 {
			t.Fatalf("loads %d interest users %v before auth", h.loader.calls.Load(), h.interestUsers())
		}
		tc.close()
	})
}

func TestAFailedRoomLoadSendsLoadFailedAndLoadRoomsAsksAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.loader.fail(errors.New("core down"))
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		if f := tc.nextFrame(); f.GetLoadFailed() == nil || f.GetLoadFailed().GetRetryAfterMs() == 0 {
			t.Fatalf("frame = %v, want load_failed with a retry hint", f)
		}
		if !tc.isOpen() || h.closes(CodeTryAgainLater) != 0 || h.interestUsers() != 1 {
			t.Fatalf("open %v closes 1013 = %d, interest users = %v", tc.isOpen(), h.closes(CodeTryAgainLater), h.interestUsers())
		}
		h.loader.fail(nil)
		h.loader.set("t1", "u1", interest.Room{ID: 7, Ver: 1})
		tc.loadRooms()
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("frame after load_rooms = %v, want ready", f)
		}
		if h.loader.calls.Load() != 2 {
			t.Fatalf("loads = %d, want 2", h.loader.calls.Load())
		}
		tc.close()
	})
}

func TestEventsAndRoomFramesFlowThroughTheInterestTable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.loader.set("t1", "u1", interest.Room{ID: 7, Ver: 1})
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.nextFrame()
		h.rooms.Route(roomEvent("msg_created", "message", 7, "7-0-1"))
		if f := tc.nextFrame(); f.GetEvent().GetId() != "7-0-1" {
			t.Fatalf("frame = %v, want event 7-0-1", f)
		}
		h.rooms.Route(roomEvent("reaction_changed", "message", 7, "7-0-1-r1"))
		tc.openRoom("7")
		synctest.Wait()
		h.rooms.Route(roomEvent("reaction_changed", "message", 7, "7-0-1-r2"))
		if f := tc.nextFrame(); f.GetEvent().GetId() != "7-0-1-r2" {
			t.Fatalf("frame = %v, want only the reaction after open_room", f)
		}
		tc.closeRoom("7")
		tc.openRoom("x")
		if f := tc.nextFrame(); f.GetRoomClosed().GetRoomId() != "x" ||
			f.GetRoomClosed().GetReason() != chatimv1.RoomClosedReason_ROOM_CLOSED_REASON_NOT_MEMBER {
			t.Fatalf("frame = %v, want room_closed x not_member", f)
		}
		h.rooms.Route(roomEvent("reaction_changed", "message", 7, "7-0-1-r3"))
		synctest.Wait()
		tc.noFrame()
		tc.close()
	})
}
