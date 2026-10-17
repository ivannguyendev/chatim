package projection_test

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestDecodeReadsTheRoomAndTimeOfAnEvent(t *testing.T) {
	ev, err := projection.Decode(encoded(t, msgCreated(roomA, 3, 9, at(5))))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if ev.Room != roomA || !ev.At.Equal(at(5)) || ev.Proto.GetThreadRoot() != 3 || ev.Proto.GetSeq() != 9 || ev.Proto.GetMessageCreated() == nil {
		t.Errorf("Decode = room %d at %v proto %v; want room A at 5s with the msg_created payload", ev.Room, ev.At, ev.Proto)
	}
}

func TestDecodeRefusesMalformedEvents(t *testing.T) {
	cases := map[string]func() *chatimv1.Event{
		"no room":   func() *chatimv1.Event { ev := msgCreated(roomA, 0, 1, at(1)); ev.RoomId = ""; return ev },
		"zero room": func() *chatimv1.Event { ev := msgCreated(roomA, 0, 1, at(1)); ev.RoomId = "0"; return ev },
		"text room": func() *chatimv1.Event { ev := msgCreated(roomA, 0, 1, at(1)); ev.RoomId = "abc"; return ev },
		"no time":   func() *chatimv1.Event { ev := msgCreated(roomA, 0, 1, at(1)); ev.Ts = nil; return ev },
		"invalid time": func() *chatimv1.Event {
			ev := msgCreated(roomA, 0, 1, at(1))
			ev.Ts = &timestamppb.Timestamp{Nanos: -1}
			return ev
		},
		"no payload": func() *chatimv1.Event { ev := msgCreated(roomA, 0, 1, at(1)); ev.Payload = nil; return ev },
		"epoch time": func() *chatimv1.Event {
			ev := msgCreated(roomA, 0, 1, at(1))
			ev.Ts = &timestamppb.Timestamp{Nanos: 5}
			return ev
		},
		"before epoch": func() *chatimv1.Event {
			ev := msgCreated(roomA, 0, 1, at(1))
			ev.Ts = &timestamppb.Timestamp{Seconds: -60}
			return ev
		},
	}
	for name, build := range cases {
		if _, err := projection.Decode(encoded(t, build())); !errors.Is(err, projection.ErrBadEvent) {
			t.Errorf("%s: Decode = %v, want ErrBadEvent", name, err)
		}
	}
	if _, err := projection.Decode([]byte{0xff, 0xff, 0xff}); !errors.Is(err, projection.ErrBadEvent) {
		t.Errorf("garbage: Decode = %v, want ErrBadEvent", err)
	}
}
