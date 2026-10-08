package pbconv

import (
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func PinEventID(room, pv uint64) string { return RoomID(room) + "-p" + strconv.FormatUint(pv, 10) }

func MessagePinned(roomType domain.RoomType, m domain.Message, a domain.PinAction) *chatimv1.Event {
	ev := pinEvent(roomType, a)
	ev.Payload = &chatimv1.Event_MessagePinned{MessagePinned: &chatimv1.MessagePinned{Message: Message(m), PinVer: a.PV}}
	return ev
}

func MessageUnpinned(roomType domain.RoomType, m domain.Message, a domain.PinAction) *chatimv1.Event {
	ev := pinEvent(roomType, a)
	ev.Payload = &chatimv1.Event_MessageUnpinned{MessageUnpinned: &chatimv1.MessageUnpinned{Message: Message(m), PinVer: a.PV}}
	return ev
}

func PinChanged(roomType domain.RoomType, m domain.Message, a domain.PinAction) *chatimv1.Event {
	if m.Deleted {
		m.Text = ""
	}
	if a.Op == domain.PinOpUnpin {
		return MessageUnpinned(roomType, m, a)
	}
	return MessagePinned(roomType, m, a)
}

func Pins(pins []domain.Pin) []*chatimv1.Pin {
	out := make([]*chatimv1.Pin, len(pins))
	for i, p := range pins {
		out[i] = &chatimv1.Pin{ThreadRoot: p.Thread, Seq: p.Seq, By: p.By, PinnedAt: timestamppb.New(p.At), PinVer: p.PV}
	}
	return out
}

func pinEvent(roomType domain.RoomType, a domain.PinAction) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         PinEventID(a.Room, a.PV),
		Tenant:     a.Tenant,
		RoomId:     RoomID(a.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: a.Thread,
		Seq:        a.Seq,
		Actor:      a.By,
		Ts:         timestamppb.New(a.At),
	}
}
