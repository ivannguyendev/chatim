package actor_test

import (
	"testing"
	"testing/synctest"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
)

func TestOnlyCommittedMessagesArePublishedInSeqOrder(t *testing.T) {
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
