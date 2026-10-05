package publish_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestOnlyMessageCreatedEventsGetAnAckMark(t *testing.T) {
	created := events(roomA, 7)[0]
	if key, ok := publish.MarkKey(roomA, created); !ok || key != (store.MsgKey{Room: roomA, Seq: 7}) {
		t.Fatalf("MarkKey(msg_created) = %v, %v; want {%d 0 7}, true", key, ok, roomA)
	}
	other := &chatimv1.Event{Id: "101-0-7-v2", Seq: 7}
	if key, ok := publish.MarkKey(roomA, other); ok {
		t.Fatalf("MarkKey(event without a msg_created payload) = %v, true; want no mark", key)
	}
}

func TestMarkDeadlineCoversAckTimeoutMarkWindowAndMarkTimeout(t *testing.T) {
	want := 2*time.Second + 10*time.Millisecond + time.Second
	if got := publish.MarkDeadline(2 * time.Second); got != want {
		t.Fatalf("MarkDeadline(2s) = %v, want %v", got, want)
	}
}
