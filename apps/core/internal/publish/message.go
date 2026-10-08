package publish

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	msgCreated            = "msg_created"
	roomCreated           = "room_created"
	msgEdited             = "msg_edited"
	msgDeleted            = "msg_deleted"
	reactionChanged       = "reaction_changed"
	countsChanged         = "counts_changed"
	msgPinned             = "msg_pinned"
	msgUnpinned           = "msg_unpinned"
	memberAdded           = "member_added"
	memberRemoved         = "member_removed"
	memberRoleChanged     = "member_role_changed"
	memberPriorityChanged = "member_priority_changed"
	memberCountChanged    = "member_count_changed"
	readUpdated           = "read_updated"
	messageHidden         = "message_hidden"
	historyCleared        = "history_cleared"

	roomData    = "room"
	memberData  = "member"
	messageData = "message"
)

var errMalformed = errors.New("event needs an id and a subject-safe tenant")

func Message(root string, room uint64, ev *chatimv1.Event) (*nats.Msg, error) {
	if ev.GetId() == "" || !validToken(ev.GetTenant()) {
		return nil, errMalformed
	}
	subject, err := subjectFor(root, ev.GetTenant(), room, eventKind(ev))
	if err != nil {
		return nil, err
	}
	data, err := proto.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	m := &nats.Msg{Subject: subject, Data: data, Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, ev.GetId())
	return m, nil
}

func subjectFor(root, tenant string, room uint64, kind string) (string, error) {
	var data string
	switch kind {
	case roomCreated, msgPinned, msgUnpinned, memberCountChanged:
		data = roomData
	case memberAdded, memberRemoved, memberRoleChanged, memberPriorityChanged, readUpdated, messageHidden, historyCleared:
		data = memberData
	case msgCreated, msgEdited, msgDeleted, reactionChanged, countsChanged:
		data = messageData
	default:
		return "", fmt.Errorf("%w: unknown event kind %q", apperr.ErrInvalidArgument, kind)
	}
	return root + "." + tenant + "." + data + "." + strconv.FormatUint(room, 10) + "." + kind, nil
}

func eventKind(ev *chatimv1.Event) string {
	switch ev.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		return msgCreated
	case *chatimv1.Event_RoomCreated:
		return roomCreated
	case *chatimv1.Event_MessageEdited:
		return msgEdited
	case *chatimv1.Event_MessageDeleted:
		return msgDeleted
	case *chatimv1.Event_ReactionChanged:
		return reactionChanged
	case *chatimv1.Event_CountsChanged:
		return countsChanged
	case *chatimv1.Event_MessagePinned:
		return msgPinned
	case *chatimv1.Event_MessageUnpinned:
		return msgUnpinned
	case *chatimv1.Event_MemberAdded:
		return memberAdded
	case *chatimv1.Event_MemberRemoved:
		return memberRemoved
	case *chatimv1.Event_MemberRoleChanged:
		return memberRoleChanged
	case *chatimv1.Event_MemberPriorityChanged:
		return memberPriorityChanged
	case *chatimv1.Event_MemberCountChanged:
		return memberCountChanged
	case *chatimv1.Event_ReadUpdated:
		return readUpdated
	case *chatimv1.Event_MessageHidden:
		return messageHidden
	case *chatimv1.Event_HistoryCleared:
		return historyCleared
	default:
		return ""
	}
}
