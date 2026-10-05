package e2e_test

import (
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func TestEventOfKeepsOnlyMessageCreatedEvents(t *testing.T) {
	const subject = "live.e2e.room.42.evt.msg_created"
	created := &chatimv1.Event{
		Id: "42-0-3", RoomId: "42", Seq: 3,
		Payload: &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{Message: &chatimv1.Message{Cid: "a-c"}}},
	}
	want := e2e.Event{Room: "42", ID: "42-0-3", Seq: 3, CID: "a-c", Subject: subject}
	if got, ok := e2e.EventOf(subject, created); !ok || got != want {
		t.Fatalf("EventOf(msg_created) = %+v, %v; want %+v, true", got, ok, want)
	}
	room := &chatimv1.Event{
		Id: "42-created", RoomId: "42",
		Payload: &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{Room: &chatimv1.Room{Id: "42"}}},
	}
	if got, ok := e2e.EventOf("live.e2e.room.42.evt.room_created", room); ok {
		t.Fatalf("EventOf(room_created) = %+v, true; want it skipped", got)
	}
	if got, ok := e2e.EventOf(subject, &chatimv1.Event{Id: "42-0-4", RoomId: "42", Seq: 4}); ok {
		t.Fatalf("EventOf(no payload) = %+v, true; want it skipped", got)
	}
}
