package plan_test

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	sentAt   = time.UnixMilli(1_700_000_000_000).UTC()
	editedAt = sentAt.Add(7 * time.Minute)
)

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

func userTarget(id string, at *timestamppb.Timestamp) *chatimv1.MentionTarget {
	return &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: id, At: at}
}

func groupTarget(id string, at *timestamppb.Timestamp) *chatimv1.MentionTarget {
	return &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: id, At: at}
}

func message(ver uint32, targets ...*chatimv1.MentionTarget) *chatimv1.Message {
	return &chatimv1.Message{RoomId: "777", Seq: 41, Sender: "Lan", CreatedAt: ts(sentAt), Ver: ver, MentionTargets: targets}
}

func envelope(at time.Time) *chatimv1.Event {
	return &chatimv1.Event{Id: "777-0-41", Tenant: "t1", RoomId: "777", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP, Seq: 41, Actor: "Lan", Ts: ts(at)}
}

func created(m *chatimv1.Message) *chatimv1.Event {
	ev := envelope(sentAt)
	ev.Payload = &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{Message: m}}
	return ev
}

func edited(ver uint32, m *chatimv1.Message, previous *chatimv1.MentionSet) *chatimv1.Event {
	ev := envelope(editedAt)
	ev.Payload = &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{Message: m, Ver: ver, PreviousMentions: previous}}
	return ev
}

func deleted(ver uint32, previous *chatimv1.MentionSet) *chatimv1.Event {
	ev := envelope(editedAt)
	m := message(ver)
	m.Deleted = true
	ev.Payload = &chatimv1.Event_MessageDeleted{MessageDeleted: &chatimv1.MessageDeleted{Message: m, Ver: ver, PreviousMentions: previous}}
	return ev
}
