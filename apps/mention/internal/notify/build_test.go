package notify_test

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/notify"
	"github.com/ivannguyendev/chatim/apps/mention/internal/plan"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestNotifiedCarriesSnapshotTargetAndDeterministicID(t *testing.T) {
	msg := snapshot()
	target := domain.Target{Kind: domain.TargetGroup, ID: "team-sale", At: editedAt}
	n, err := notify.Notified("notify", result(plan.Edited, 1), msg, "Minh", target)
	noErr(t, err)
	equal(t, notify.KindNotified, n.Kind)
	equal(t, "notify.t1.Minh.mention", n.Subject)
	want := &chatimv1.Notification{
		Id: "777-0-41-Minh-m1", Tenant: "t1", User: "Minh", RoomId: "777",
		RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP, Seq: 41, MessageVer: 1, Ts: timestamppb.New(editedAt),
		Payload: &chatimv1.Notification_MentionNotified{MentionNotified: &chatimv1.MentionNotified{
			Message: msg,
			Target:  &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "team-sale", At: timestamppb.New(editedAt)},
		}},
	}
	truth(t, proto.Equal(want, n.Notification), n.Notification.String())
}

func TestNotifiedTargetKinds(t *testing.T) {
	cases := map[string]struct {
		target domain.Target
		want   *chatimv1.MentionTarget
	}{
		"user":  {domain.Target{Kind: domain.TargetUser, ID: "Minh", At: sentAt}, &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "Minh", At: timestamppb.New(sentAt)}},
		"all":   {domain.Target{Kind: domain.TargetAll, At: sentAt}, &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_ALL, At: timestamppb.New(sentAt)}},
		"no at": {domain.Target{Kind: domain.TargetUser, ID: "Minh"}, &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "Minh"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			n, err := notify.Notified("notify", result(plan.Created, 0), snapshot(), "Minh", c.target)
			noErr(t, err)
			truth(t, proto.Equal(c.want, n.Notification.GetMentionNotified().GetTarget()))
			equal(t, "777-0-41-Minh-m0", n.Notification.GetId())
		})
	}
}

func TestRetractedReasonByKind(t *testing.T) {
	cases := map[plan.Kind]chatimv1.RetractReason{
		plan.Edited:  chatimv1.RetractReason_RETRACT_REASON_EDITED,
		plan.Deleted: chatimv1.RetractReason_RETRACT_REASON_DELETED,
	}
	for kind, reason := range cases {
		r := result(kind, 3)
		r.Plan.Key.Thread = 9
		r.RoomType = domain.RoomDM
		n, err := notify.Retracted("notify", r, "Minh")
		noErr(t, err)
		equal(t, notify.KindRetracted, n.Kind)
		equal(t, "notify.t1.Minh.mention", n.Subject)
		equal(t, "777-9-41-Minh-r3", n.Notification.GetId())
		equal(t, reason, n.Notification.GetMentionRetracted().GetReason())
		equal(t, chatimv1.RoomType_ROOM_TYPE_DM, n.Notification.GetRoomType())
		equal(t, uint64(9), n.Notification.GetThreadRoot())
		equal(t, uint32(3), n.Notification.GetMessageVer())
	}
}

func TestChannelRoomHasNoProtoType(t *testing.T) {
	r := result(plan.Created, 0)
	r.RoomType = domain.RoomChannel
	n, err := notify.Notified("notify", r, snapshot(), "Minh", domain.Target{Kind: domain.TargetUser, ID: "Minh"})
	noErr(t, err)
	equal(t, chatimv1.RoomType_ROOM_TYPE_UNSPECIFIED, n.Notification.GetRoomType())
}

func TestBuildRejects(t *testing.T) {
	_, err := notify.Retracted("notify", result(plan.Created, 0), "Minh")
	truth(t, errors.Is(err, apperr.ErrInvalidArgument))
	_, err = notify.Retracted("notify", result(plan.Edited, 1), "Minh.x")
	truth(t, errors.Is(err, apperr.ErrInvalidArgument))
	r := result(plan.Edited, 1)
	r.Plan.Tenant = "T*"
	_, err = notify.Notified("notify", r, snapshot(), "Minh", domain.Target{Kind: domain.TargetUser, ID: "Minh"})
	truth(t, errors.Is(err, apperr.ErrInvalidArgument))
}
