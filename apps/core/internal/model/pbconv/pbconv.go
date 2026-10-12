package pbconv

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errUnknownRoomType = fmt.Errorf("%w: type", apperr.ErrInvalidArgument)

func RoomID(room uint64) string { return strconv.FormatUint(room, 10) }

func MessageEventID(room, thread, seq uint64) string {
	return RoomID(room) + "-" + strconv.FormatUint(thread, 10) + "-" + strconv.FormatUint(seq, 10)
}

func RoomCreatedEventID(room uint64) string { return RoomID(room) + "-created" }

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

func DomainRoomType(t chatimv1.RoomType) (domain.RoomType, error) {
	switch t {
	case chatimv1.RoomType_ROOM_TYPE_DM:
		return domain.RoomDM, nil
	case chatimv1.RoomType_ROOM_TYPE_GROUP:
		return domain.RoomGroup, nil
	default:
		return "", errUnknownRoomType
	}
}

func Room(r domain.Room) *chatimv1.Room {
	return &chatimv1.Room{
		Id:          RoomID(r.ID),
		Tenant:      r.Tenant,
		Type:        RoomType(r.Type),
		Name:        r.Name,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   timestamppb.New(r.CreatedAt),
		MemberCount: memberCount(r.MemberCount),
	}
}

func memberCount(n int) int32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(n)
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
	m = m.WithoutDeletedContent()
	return &chatimv1.Message{
		RoomId:         RoomID(m.Room),
		ThreadRoot:     m.Thread,
		Seq:            m.Seq,
		Sender:         m.From,
		Kind:           MessageKind(m.Kind),
		Text:           m.Text,
		Cid:            m.CID,
		CreatedAt:      timestamppb.New(m.CreatedAt),
		Ver:            m.Version,
		Deleted:        m.Deleted,
		EditedAt:       optionalTime(m.EditedAt),
		Hidden:         m.Hidden,
		Reactions:      ReactionSummary(m.Reactions),
		ReplyTo:        ReplyRef(m.ReplyTo),
		ForwardFrom:    ForwardRef(m.Forward),
		MentionTargets: MentionTargets(m.Mentions),
		MentionAll:     m.MentionAll,
		ReplyCount:     ReplyCount(m.Replies),
	}
}

func optionalTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func MessageCreated(roomType domain.RoomType, m domain.Message) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         MessageEventID(m.Room, m.Thread, m.Seq),
		Tenant:     m.Tenant,
		RoomId:     RoomID(m.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: m.Thread,
		Seq:        m.Seq,
		Actor:      m.From,
		Ts:         timestamppb.New(m.CreatedAt),
		Payload:    &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{Message: Message(m)}},
	}
}

func RoomCreated(r domain.Room) *chatimv1.Event {
	return &chatimv1.Event{
		Id:       RoomCreatedEventID(r.ID),
		Tenant:   r.Tenant,
		RoomId:   RoomID(r.ID),
		RoomType: RoomType(r.Type),
		Actor:    r.CreatedBy,
		Ts:       timestamppb.New(r.CreatedAt),
		Payload:  &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{Room: Room(r)}},
	}
}
