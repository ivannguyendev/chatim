package e2e_test

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

type feedRun struct {
	feed   *e2e.WSFeed
	frames chan *chatimv1.ServerFrame
	loads  atomic.Int32
	done   chan struct{}
}

func startFeed(t *testing.T) *feedRun {
	t.Helper()
	r := &feedRun{feed: e2e.NewWSFeed(), frames: make(chan *chatimv1.ServerFrame), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		r.feed.Run(r.frames, func() error {
			r.loads.Add(1)
			return nil
		})
	}()
	t.Cleanup(func() {
		close(r.frames)
		<-r.done
	})
	return r
}

func (r *feedRun) send(frames ...*chatimv1.ServerFrame) {
	for _, f := range frames {
		r.frames <- f
	}
	synctest.Wait()
}

func eventOf(ev *chatimv1.Event) *chatimv1.ServerFrame {
	return &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Event{Event: ev}}
}

func typingOf(room, user string, stopped bool) *chatimv1.ServerFrame {
	return &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Typing{Typing: &chatimv1.TypingNotice{RoomId: room, UserId: user, Stopped: stopped}}}
}

func loadFailed(ms uint32) *chatimv1.ServerFrame {
	return &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_LoadFailed{LoadFailed: &chatimv1.LoadFailedFrame{RetryAfterMs: ms}}}
}

func readyFrame() *chatimv1.ServerFrame {
	return &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Ready{Ready: &chatimv1.ReadyFrame{}}}
}

func sentBy(ev *chatimv1.Event, actor string) *chatimv1.Event {
	ev.Actor = actor
	return ev
}

func frames(entries []e2e.WSEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Frame+":"+e.Kind)
	}
	return out
}

func TestWSFeedRunRecordsFreshEventsAndGapsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startFeed(t)
		r.send(readyFrame(), eventOf(created("7", 1)), eventOf(created("7", 1)), eventOf(created("7", 4)))
		got := r.feed.Entries(0)
		want := []string{"ready:", "event:message_created", "gap:", "event:message_created"}
		if len(got) != len(want) {
			t.Fatalf("entries %v, want %v", frames(got), want)
		}
		for i, w := range want {
			if frames(got)[i] != w {
				t.Fatalf("entries %v, want %v", frames(got), want)
			}
		}
		if got[2].Room != "7" || got[2].Seq != 2 || got[2].To != 3 {
			t.Fatalf("gap entry %+v, want 2..3 of room 7", got[2])
		}
		if s := r.feed.Stats(); s.Delivered != 2 || s.Duplicates != 1 || s.Gaps != 1 {
			t.Fatalf("stats = %+v", s)
		}
	})
}

func TestWSFeedRunRecordsNotificationsResyncsClosesAndPresence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startFeed(t)
		r.send(
			&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Notification{Notification: &chatimv1.Notification{Id: "n1", RoomId: "7", Payload: &chatimv1.Notification_MentionNotified{MentionNotified: &chatimv1.MentionNotified{}}}}},
			&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Resync{Resync: &chatimv1.ResyncFrame{Reason: chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD}}},
			&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_RoomClosed{RoomClosed: &chatimv1.RoomClosedFrame{RoomId: "7", Reason: chatimv1.RoomClosedReason_ROOM_CLOSED_REASON_REMOVED}}},
			&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Presence{Presence: &chatimv1.PresenceFrame{Entries: []*chatimv1.PresenceEntry{presence("a", true)}}}},
		)
		checks := []e2e.WSMatch{
			{Frame: e2e.FrameNotification, Kind: "mention_notified", ID: "n1", Room: "7"},
			{Frame: e2e.FrameResync, Kind: chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD.String()},
			{Frame: e2e.FrameRoomClosed, Kind: chatimv1.RoomClosedReason_ROOM_CLOSED_REASON_REMOVED.String(), Room: "7"},
			e2e.PresenceMatch("a", true),
		}
		for want, m := range checks {
			if i, _, ok := r.feed.Find(0, m); !ok || i != want {
				t.Fatalf("Find(%v) = %d, %v, want %d", m, i, ok, want)
			}
		}
	})
}

func TestWSFeedRunEndsTypingOnStopSilenceOrTheTypistsMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startFeed(t)
		r.send(typingOf("7", "a", false), typingOf("7", "b", false), typingOf("7", "c", true))
		r.send(typingOf("7", "b", true))
		if !r.ended("b", 0) || r.ended("c", 0) {
			t.Fatal("a stop notice did not end b, or ended c who never typed")
		}
		r.send(eventOf(sentBy(created("7", 1), "a")))
		i, _, ok := r.feed.Find(0, e2e.TypingMatch("7", "a", false))
		if j, _, _ := r.feed.Find(0, e2e.WSMatch{Kind: "message_created"}); !ok || i != j-1 {
			t.Fatalf("a's typing end at %d, %v, want right before its message at %d", i, ok, j)
		}
		r.send(typingOf("7", "d", false))
		mark := r.feed.Len()
		time.Sleep(e2e.TypingTimeout - time.Millisecond)
		synctest.Wait()
		if r.ended("d", mark) {
			t.Fatal("d's typing ended before six seconds")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if !r.ended("d", mark) {
			t.Fatal("d's typing did not end after six seconds of silence")
		}
	})
}

func (r *feedRun) ended(user string, from int) bool {
	_, _, ok := r.feed.Find(from, e2e.TypingMatch("7", user, false))
	return ok
}

func TestWSFeedRunLoadsRoomsAfterTheRetryHintUnlessReadyComes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startFeed(t)
		r.send(loadFailed(1000))
		time.Sleep(1250*time.Millisecond - time.Nanosecond)
		synctest.Wait()
		if n := r.loads.Load(); n != 0 {
			t.Fatalf("loaded %d times before the hint plus jitter", n)
		}
		time.Sleep(250*time.Millisecond + time.Nanosecond)
		synctest.Wait()
		if n := r.loads.Load(); n != 1 {
			t.Fatalf("loaded %d times after the hint plus jitter, want 1", n)
		}
		r.send(loadFailed(1000), readyFrame())
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if n := r.loads.Load(); n != 1 {
			t.Fatalf("loaded %d times after ready, want no new load", n)
		}
	})
}
