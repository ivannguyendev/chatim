package e2e_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func memberAdded(room, user string) *chatimv1.Event {
	return &chatimv1.Event{Id: room + "-mb-" + user + "-v1", RoomId: room, Payload: &chatimv1.Event_MemberAdded{
		MemberAdded: &chatimv1.MemberAdded{User: user, Ver: 1},
	}}
}

func created(room string, seq uint64) *chatimv1.Event {
	return &chatimv1.Event{Id: e2e.MessageEventID(room, seq), RoomId: room, Seq: seq, Payload: &chatimv1.Event_MessageCreated{
		MessageCreated: &chatimv1.MessageCreated{Message: &chatimv1.Message{RoomId: room, Seq: seq}},
	}}
}

func TestWSFeedRecordsEventKindAndPayloadUser(t *testing.T) {
	f := e2e.NewWSFeed()
	f.Event(memberAdded("7", "bob"))
	f.Event(created("7", 3))
	_, got, ok := f.Find(0, e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "member_added", Room: "7"})
	if !ok || got.User != "bob" || got.ID != "7-mb-bob-v1" {
		t.Fatalf("member_added entry = %+v, %v", got, ok)
	}
	i, got, ok := f.Find(0, e2e.WSMatch{Kind: "message_created", Seq: 3})
	if !ok || i != 1 || got.User != "" || got.ID != "7-0-3" {
		t.Fatalf("message_created entry %d = %+v, %v", i, got, ok)
	}
}

func TestWSFeedRecordsNotificationsClosesResyncsAndGaps(t *testing.T) {
	f := e2e.NewWSFeed()
	f.Ready()
	f.Notification(&chatimv1.Notification{Id: "n1", RoomId: "7", Seq: 2, Payload: &chatimv1.Notification_MentionNotified{MentionNotified: &chatimv1.MentionNotified{}}})
	f.RoomClosed("7", chatimv1.RoomClosedReason_ROOM_CLOSED_REASON_REMOVED)
	f.Resync(chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD)
	f.Gap(e2e.Gap{Room: "7", From: 4, To: 5})
	checks := []e2e.WSMatch{
		{Frame: e2e.FrameReady},
		{Frame: e2e.FrameNotification, Kind: "mention_notified", ID: "n1", Room: "7", Seq: 2},
		{Frame: e2e.FrameRoomClosed, Kind: chatimv1.RoomClosedReason_ROOM_CLOSED_REASON_REMOVED.String(), Room: "7"},
		{Frame: e2e.FrameResync, Kind: chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD.String()},
		{Frame: e2e.FrameGap, Room: "7", Seq: 4},
	}
	for want, m := range checks {
		if i, _, ok := f.Find(0, m); !ok || i != want {
			t.Fatalf("Find(%v) = %d, %v, want %d", m, i, ok, want)
		}
	}
	if f.Len() != len(checks) {
		t.Fatalf("Len = %d, want %d", f.Len(), len(checks))
	}
	tail := f.Entries(4)
	if len(tail) != 1 || tail[0].To != 5 {
		t.Fatalf("Entries(4) = %+v, want the gap ending at 5", tail)
	}
	if f.Entries(9) != nil {
		t.Fatal("Entries past the end returned entries")
	}
}

func TestWSFeedFindStartsAtTheMarkAndCountsEveryMatch(t *testing.T) {
	f := e2e.NewWSFeed()
	f.Event(created("7", 1))
	f.Event(created("8", 1))
	f.Event(created("7", 2))
	if _, _, ok := f.Find(3, e2e.WSMatch{Room: "7"}); ok {
		t.Fatal("Find past the end found an entry")
	}
	if i, _, ok := f.Find(1, e2e.WSMatch{Room: "7"}); !ok || i != 2 {
		t.Fatalf("Find from 1 = %d, %v, want 2", i, ok)
	}
	if n := f.Count(e2e.WSMatch{Room: "7"}); n != 2 {
		t.Fatalf("Count = %d, want 2", n)
	}
}

func TestWSFeedWaitReturnsWhenTheEntryArrives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := e2e.NewWSFeed()
		f.Event(created("7", 1))
		got := make(chan int, 1)
		go func() {
			i, err := f.Wait(t.Context(), 1, e2e.WSMatch{Room: "7"})
			if err != nil {
				t.Errorf("Wait = %v", err)
			}
			got <- i
		}()
		synctest.Wait()
		f.Event(created("8", 1))
		f.Event(created("7", 2))
		if i := <-got; i != 2 {
			t.Fatalf("Wait = %d, want 2", i)
		}
	})
}

func TestWSFeedWaitStopsAtTheDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := e2e.NewWSFeed()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err := f.Wait(ctx, 0, e2e.WSMatch{Room: "7"}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait = %v, want deadline exceeded", err)
		}
	})
}
