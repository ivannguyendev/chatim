package actor_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
)

const markEvery = 5 * time.Second

func TestActiveMarkIsWrittenBeforeTheInsert(t *testing.T) {
	rg := started(t, baseConfig)
	mustSend(t, rg.Router, cmd(roomA, "alice", "a1"))
	mustSend(t, rg.Router, cmd(roomB, "alice", "b1"))
	if got, want := rg.order.list(), []string{"mark", "submit", "mark", "submit"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestActiveMarkIsThrottledPerRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		mustSend(t, rg.Router, cmd(roomA, "alice", "a1"))
		time.Sleep(markEvery - time.Millisecond)
		mustSend(t, rg.Router, cmd(roomA, "alice", "a2"))
		if n := rg.marks.attempts(roomA); n != 1 {
			t.Fatalf("marked room A %d times within %v, want 1", n, markEvery)
		}
		mustSend(t, rg.Router, cmd(roomB, "alice", "b1"))
		if n := rg.marks.attempts(roomB); n != 1 {
			t.Fatalf("room B marks = %d, want its own first mark", n)
		}
		time.Sleep(time.Millisecond)
		mustSend(t, rg.Router, cmd(roomA, "alice", "a3"))
		if n := rg.marks.attempts(roomA); n != 2 {
			t.Fatalf("marked room A %d times after %v, want 2", n, markEvery)
		}
		rg.cancel()
		_ = rg.wait()
	})
}

func TestSendSucceedsWhenTheMarkFailsAndTheNextGroupRetriesIt(t *testing.T) {
	rg := started(t, baseConfig)
	rg.marks.fail(errors.New("redis down"))
	mustSend(t, rg.Router, cmd(roomA, "alice", "a1"))
	mustSend(t, rg.Router, cmd(roomA, "alice", "a2"))
	if n := rg.marks.attempts(roomA); n != 2 {
		t.Fatalf("mark attempts after two failing groups = %d, want 2", n)
	}
	rg.marks.fail(nil)
	mustSend(t, rg.Router, cmd(roomA, "alice", "a3"))
	mustSend(t, rg.Router, cmd(roomA, "alice", "a4"))
	if n := rg.marks.attempts(roomA); n != 3 {
		t.Fatalf("mark attempts after a success = %d, want 3", n)
	}
	if n := len(timeline(t, rg.msgs, roomA)); n != 4 {
		t.Fatalf("stored %d messages, want 4", n)
	}
}

func TestOnlyCommittedMessagesArePublishedAndInPtsOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		mustSend(t, rg.Router, cmd(roomA, "alice", "a1"))
		rg.sub.then(rejected)
		if _, err := rg.Send(t.Context(), cmd(roomA, "alice", "lost")); err == nil {
			t.Fatal("rejected message acked")
		}
		rg.sub.then(rg.sub.landedUnknown)
		mustSend(t, rg.Router, cmd(roomA, "bob", "a2"))
		rg.sub.hold()
		waits := []<-chan sendResult{
			sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "a3")),
			sendAsync(t.Context(), rg.Router, cmd(roomA, "bob", "a4")),
			sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "a5")),
		}
		synctest.Wait()
		if n := len(rg.sub.sent()); n != 4 {
			t.Fatalf("submitted %d groups before the held one finished, want 4", n)
		}
		rg.sub.open()
		for _, w := range waits {
			if r := <-w; r.err != nil {
				t.Fatalf("Send: %v", r.err)
			}
		}
		synctest.Wait()

		docs := timeline(t, rg.msgs, roomA)
		got := rg.events.events(roomA)
		if len(got) != len(docs) || len(docs) != 5 {
			t.Fatalf("published %d events for %d stored messages, want 5 each", len(got), len(docs))
		}
		for i, doc := range docs {
			if want := pbconv.MessageCreated(domain.RoomGroup, doc); !proto.Equal(got[i], want) {
				t.Fatalf("event %d = %v, want %v", i, got[i], want)
			}
		}
		if n := len(rg.events.events(roomB)); n != 0 {
			t.Fatalf("published %d events for an idle room", n)
		}
		rg.cancel()
		_ = rg.wait()
	})
}
