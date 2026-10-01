package pbconv

import (
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func RoomID(room uint64) string { return strconv.FormatUint(room, 10) }

func EventID(room, pts uint64) string { return RoomID(room) + "-" + strconv.FormatUint(pts, 10) }

func RoomType(t domain.RoomType) chatimv1.RoomType {
	switch t {
	case domain.RoomDM:
		return chatimv1.RoomType_ROOM_TYPE_DM
	case domain.RoomGroup:
		return chatimv1.RoomType_ROOM_TYPE_GROUP
	default:
		return chatimv1.RoomType_ROOM_TYPE_UNSPECIFIED
	}
}

func MessageKind(k domain.Kind) chatimv1.MessageKind {
	switch k {
	case domain.KindText:
		return chatimv1.MessageKind_MESSAGE_KIND_TEXT
	default:
		return chatimv1.MessageKind_MESSAGE_KIND_UNSPECIFIED
	}
}

func Message(m domain.Message) *chatimv1.Message {
	return &chatimv1.Message{
		RoomId:     RoomID(m.Room),
		ThreadRoot: m.Thread,
		Seq:        m.Seq,
		Pts:        m.Pts,
		Sender:     m.From,
		Kind:       MessageKind(m.Kind),
		Text:       m.Text,
		Cid:        m.CID,
		CreatedAt:  timestamppb.New(m.CreatedAt),
	}
}

func MessageCreated(roomType domain.RoomType, m domain.Message) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         EventID(m.Room, m.Pts),
		Tenant:     m.Tenant,
		RoomId:     RoomID(m.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: m.Thread,
		Seq:        m.Seq,
		Pts:        m.Pts,
		Actor:      m.From,
		Ts:         timestamppb.New(m.CreatedAt),
		Payload:    &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{Message: Message(m)}},
	}
}
