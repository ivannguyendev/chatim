package notify

import (
	"fmt"
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/plan"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Kind uint8

const (
	KindNotified  Kind = 1
	KindRetracted Kind = 2
)

type Notice struct {
	Kind         Kind
	Subject      string
	Notification *chatimv1.Notification
}

func Subject(root, tenant, user string) string {
	return root + "." + tenant + "." + user + ".mention"
}

func Notified(root string, r plan.Result, msg *chatimv1.Message, user string, target domain.Target) (Notice, error) {
	n, err := envelope(r, user, 'm')
	if err != nil {
		return Notice{}, err
	}
	n.Payload = &chatimv1.Notification_MentionNotified{MentionNotified: &chatimv1.MentionNotified{Message: msg, Target: targetOf(target)}}
	return Notice{Kind: KindNotified, Subject: Subject(root, r.Plan.Tenant, user), Notification: n}, nil
}

func Retracted(root string, r plan.Result, user string) (Notice, error) {
	reason, ok := retractReasons[r.Kind]
	if !ok {
		return Notice{}, fmt.Errorf("%w: retract a mention of a created message", apperr.ErrInvalidArgument)
	}
	n, err := envelope(r, user, 'r')
	if err != nil {
		return Notice{}, err
	}
	n.Payload = &chatimv1.Notification_MentionRetracted{MentionRetracted: &chatimv1.MentionRetracted{Reason: reason}}
	return Notice{Kind: KindRetracted, Subject: Subject(root, r.Plan.Tenant, user), Notification: n}, nil
}

func ID(key domain.MessageKey, user string, mark byte, ver uint32) string {
	return strconv.FormatUint(key.Room, 10) + "-" + strconv.FormatUint(key.Thread, 10) + "-" +
		strconv.FormatUint(key.Seq, 10) + "-" + user + "-" + string(mark) + strconv.FormatUint(uint64(ver), 10)
}

var retractReasons = map[plan.Kind]chatimv1.RetractReason{
	plan.Edited:  chatimv1.RetractReason_RETRACT_REASON_EDITED,
	plan.Deleted: chatimv1.RetractReason_RETRACT_REASON_DELETED,
}

var roomTypes = map[domain.RoomType]chatimv1.RoomType{
	domain.RoomDM:    chatimv1.RoomType_ROOM_TYPE_DM,
	domain.RoomGroup: chatimv1.RoomType_ROOM_TYPE_GROUP,
}

var targetKinds = map[domain.TargetKind]chatimv1.MentionKind{
	domain.TargetUser:  chatimv1.MentionKind_MENTION_KIND_USER,
	domain.TargetGroup: chatimv1.MentionKind_MENTION_KIND_GROUP,
}

func envelope(r plan.Result, user string, mark byte) (*chatimv1.Notification, error) {
	if err := ids.ValidTenant(r.Plan.Tenant); err != nil {
		return nil, err
	}
	if err := ids.ValidUser(user); err != nil {
		return nil, err
	}
	key := r.Plan.Key
	return &chatimv1.Notification{
		Id:         ID(key, user, mark, r.Plan.Ver),
		Tenant:     r.Plan.Tenant,
		User:       user,
		RoomId:     strconv.FormatUint(key.Room, 10),
		RoomType:   roomTypes[r.RoomType],
		ThreadRoot: key.Thread,
		Seq:        key.Seq,
		MessageVer: r.Plan.Ver,
		Ts:         timestamppb.New(r.Ts),
	}, nil
}

func targetOf(t domain.Target) *chatimv1.MentionTarget {
	out := &chatimv1.MentionTarget{Kind: targetKinds[t.Kind], Id: t.ID}
	if !t.At.IsZero() {
		out.At = timestamppb.New(t.At)
	}
	return out
}
