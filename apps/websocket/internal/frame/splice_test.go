package frame

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func decodeServerFrame(t *testing.T, raw []byte) *chatimv1.ServerFrame {
	t.Helper()
	var got chatimv1.ServerFrame
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal server frame: %v", err)
	}
	return &got
}

func marshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestEventSplicesEncodedEventIntoServerFrame(t *testing.T) {
	want := &chatimv1.Event{
		Id:         "42-0-7",
		Tenant:     "t1",
		RoomId:     "42",
		RoomType:   chatimv1.RoomType_ROOM_TYPE_GROUP,
		ThreadRoot: 0,
		Seq:        7,
		Ts:         timestamppb.Now(),
	}
	got := decodeServerFrame(t, Event(marshal(t, want))).GetEvent()
	if got == nil {
		t.Fatal("frame carries no event")
	}
	if !proto.Equal(got, want) {
		t.Fatalf("event = %v, want %v", got, want)
	}
}

func TestNotificationSplicesEncodedNotificationIntoServerFrame(t *testing.T) {
	want := &chatimv1.Notification{Id: "n-1", Tenant: "t1", User: "u1"}
	got := decodeServerFrame(t, Notification(marshal(t, want))).GetNotification()
	if got == nil {
		t.Fatal("frame carries no notification")
	}
	if !proto.Equal(got, want) {
		t.Fatalf("notification = %v, want %v", got, want)
	}
}

func TestEmptyRawGivesFrameWithEmptyMessage(t *testing.T) {
	event := decodeServerFrame(t, Event(nil))
	if _, ok := event.GetFrame().(*chatimv1.ServerFrame_Event); !ok {
		t.Fatalf("frame = %T, want event", event.GetFrame())
	}
	if !proto.Equal(event.GetEvent(), &chatimv1.Event{}) {
		t.Fatalf("event = %v, want empty", event.GetEvent())
	}
	notification := decodeServerFrame(t, Notification([]byte{}))
	if _, ok := notification.GetFrame().(*chatimv1.ServerFrame_Notification); !ok {
		t.Fatalf("frame = %T, want notification", notification.GetFrame())
	}
}

func TestSpliceMatchesProtoMarshal(t *testing.T) {
	event := &chatimv1.Event{Id: "1-0-1", RoomId: "1", Seq: 1}
	want := marshal(t, &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Event{Event: event}})
	if got := Event(marshal(t, event)); string(got) != string(want) {
		t.Fatalf("spliced = %x, want %x", got, want)
	}
	notification := &chatimv1.Notification{Id: "n", User: "u"}
	want = marshal(t, &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Notification{Notification: notification}})
	if got := Notification(marshal(t, notification)); string(got) != string(want) {
		t.Fatalf("spliced = %x, want %x", got, want)
	}
}

func TestSpliceAllocatesExactlyOnce(t *testing.T) {
	raw := marshal(t, &chatimv1.Event{Id: "1-0-1", RoomId: "1", Seq: 1})
	allocs := testing.AllocsPerRun(100, func() {
		out := Event(raw)
		if len(out) != cap(out) {
			t.Fatalf("len %d cap %d", len(out), cap(out))
		}
	})
	if allocs != 1 {
		t.Fatalf("allocs = %v, want 1", allocs)
	}
}

func TestFieldNumbersComeFromDescriptor(t *testing.T) {
	if eventField != 21 || notificationField != 22 {
		t.Fatalf("fields = %d, %d, want 21, 22", eventField, notificationField)
	}
}

func TestOpenAndCloseRoomFramesRoundTrip(t *testing.T) {
	for _, want := range []*chatimv1.ClientFrame{
		{Frame: &chatimv1.ClientFrame_OpenRoom{OpenRoom: &chatimv1.OpenRoomFrame{RoomId: "123456789"}}},
		{Frame: &chatimv1.ClientFrame_CloseRoom{CloseRoom: &chatimv1.CloseRoomFrame{RoomId: "987654321"}}},
	} {
		var got chatimv1.ClientFrame
		if err := proto.Unmarshal(marshal(t, want), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !proto.Equal(&got, want) {
			t.Fatalf("frame = %v, want %v", &got, want)
		}
	}
	open := (&chatimv1.ClientFrame{}).ProtoReflect().Descriptor().Fields()
	if open.ByName("open_room").Number() != 20 || open.ByName("close_room").Number() != 21 {
		t.Fatal("client frame field numbers drifted")
	}
}

func TestServerControlFramesRoundTrip(t *testing.T) {
	for _, want := range []*chatimv1.ServerFrame{
		{Frame: &chatimv1.ServerFrame_Ready{Ready: &chatimv1.ReadyFrame{}}},
		{Frame: &chatimv1.ServerFrame_Resync{Resync: &chatimv1.ResyncFrame{Reason: chatimv1.ResyncReason_RESYNC_REASON_CLIENT_BACKLOG}}},
		{Frame: &chatimv1.ServerFrame_RoomClosed{RoomClosed: &chatimv1.RoomClosedFrame{RoomId: "42", Reason: chatimv1.RoomClosedReason_ROOM_CLOSED_REASON_REMOVED}}},
	} {
		if got := decodeServerFrame(t, marshal(t, want)); !proto.Equal(got, want) {
			t.Fatalf("frame = %v, want %v", got, want)
		}
	}
}
