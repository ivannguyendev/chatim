package session

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
)

func TestTypingReachesOnlyOtherUsersViewingTheRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		for _, u := range []string{"a", "b", "c"} {
			h.loader.set("t1", u, interest.Room{ID: 7, Ver: 1})
		}
		a := h.readyClient("a", "7")
		a2 := h.readyClient("a", "7")
		b := h.readyClient("b", "7")
		c := h.readyClient("c")
		synctest.Wait()
		a.typing("7", false)
		synctest.Wait()
		notice := b.nextFrame().GetTyping()
		if notice.GetRoomId() != "7" || notice.GetUserId() != "a" || notice.GetStopped() {
			t.Fatalf("b frame = %v, want typing of a in 7", notice)
		}
		b.noFrame()
		a.noFrame()
		a2.noFrame()
		c.noFrame()
		if !a.isOpen() || h.typingCount("typing_delivered_total", "") != 1 {
			t.Fatalf("open %v delivered %v, want the sender open and one delivery", a.isOpen(), h.typingCount("typing_delivered_total", ""))
		}
		for _, tc := range []*testClient{a, a2, b, c} {
			tc.close()
		}
	})
}

func TestTypingOfANonMemberIsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.loader.set("t1", "b", interest.Room{ID: 7, Ver: 1})
		b := h.readyClient("b", "7")
		d := h.readyClient("d")
		d.typing("7", false)
		d.typing("x", false)
		synctest.Wait()
		b.noFrame()
		if !d.isOpen() || h.typingCount("typing_dropped_total", "not_member") != 2 {
			t.Fatalf("open %v not_member drops %v, want open and 2", d.isOpen(), h.typingCount("typing_dropped_total", "not_member"))
		}
		b.close()
		d.close()
	})
}

func TestTypingBeforeTheRoomsAreLoadedIsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.loader.set("t1", "a", interest.Room{ID: 7, Ver: 1})
		h.loader.fail(errors.New("core down"))
		a := h.dial(true)
		a.auth(h.token("a", time.Hour))
		if f := a.nextFrame(); f.GetLoadFailed() == nil {
			t.Fatalf("frame = %v, want load_failed", f)
		}
		a.typing("7", false)
		synctest.Wait()
		if !a.isOpen() || h.typingCount("typing_dropped_total", "not_member") != 1 {
			t.Fatalf("open %v not_member drops %v, want open and 1", a.isOpen(), h.typingCount("typing_dropped_total", "not_member"))
		}
		a.close()
	})
}

func TestTypingOverItsRateIsDroppedWithoutClosing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.TypingRate, opts.TypingBurst = 1, 2
		h := newHarness(t, opts)
		h.loader.set("t1", "a", interest.Room{ID: 7, Ver: 1})
		h.loader.set("t1", "b", interest.Room{ID: 7, Ver: 1})
		a := h.readyClient("a")
		b := h.readyClient("b", "7")
		synctest.Wait()
		for range 3 {
			a.typing("7", false)
		}
		synctest.Wait()
		b.nextFrame()
		b.nextFrame()
		b.noFrame()
		if !a.isOpen() || h.typingCount("typing_dropped_total", "rate") != 1 {
			t.Fatalf("open %v rate drops %v, want open and 1", a.isOpen(), h.typingCount("typing_dropped_total", "rate"))
		}
		time.Sleep(time.Second)
		a.typing("7", true)
		if !b.nextFrame().GetTyping().GetStopped() {
			t.Fatal("typing after the refill was not delivered")
		}
		a.close()
		b.close()
	})
}

func TestTypingCountsTowardTheSharedFrameRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.CommandRate, opts.CommandBurst = 1, 3
		h := newHarness(t, opts)
		a := h.dial(true)
		a.auth(h.token("a", time.Hour))
		for range 3 {
			a.typing("7", false)
		}
		synctest.Wait()
		if got := h.frameErrors("rate"); got != 1 || !a.isOpen() {
			t.Fatalf("rate drops %v open %v", got, a.isOpen())
		}
		a.close()
	})
}
