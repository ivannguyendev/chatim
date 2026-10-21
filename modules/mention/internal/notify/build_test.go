package notify_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/notify"
	"github.com/ivannguyendev/chatim/modules/mention/internal/plan"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

var builder = notify.Builder{Root: wspublish.DefaultRoot, Page: 1000}

func recipients(n int, target domain.Target) map[string]domain.Target {
	out := make(map[string]domain.Target, n)
	for i := range n {
		out[fmt.Sprintf("u%04d", i)] = target
	}
	return out
}

func usersOf(ns []notify.Notice) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Packet.GetTargets().GetUsers()...)
	}
	return out
}

func TestNotifiedCarriesTheContentOnceForEveryRecipient(t *testing.T) {
	msg := snapshot()
	target := domain.Target{Kind: domain.TargetGroup, ID: "team-sale", At: editedAt}
	ns, err := builder.Notified(result(plan.Edited, 1), msg, map[string]domain.Target{"Minh": target, "Hoa": target})
	noErr(t, err)
	equal(t, 1, len(ns))
	n := ns[0]
	equal(t, notify.KindNotified, n.Kind)
	equal(t, "websocket.publish.t1", n.Subject)
	want := &chatimv1.Notification{
		Id: "777-0-41-m1-g:team-sale", Tenant: "t1", RoomId: "777",
		RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP, Seq: 41, MessageVer: 1, Ts: timestamppb.New(editedAt),
		Payload: &chatimv1.Notification_MentionNotified{MentionNotified: &chatimv1.MentionNotified{
			Message: msg,
			Target:  &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "team-sale", At: timestamppb.New(editedAt)},
		}},
	}
	truth(t, proto.Equal(want, n.Packet.GetNotification()), n.Packet.GetNotification().String())
	equal(t, "mention", n.Packet.GetKind())
	equal(t, "t1", n.Packet.GetTenant())
	equal(t, []string{"Hoa", "Minh"}, n.Packet.GetTargets().GetUsers())
	equal(t, uint32(1), n.Packet.GetPart())
	equal(t, uint32(1), n.Packet.GetParts())
	equal(t, wspublish.PageID("777-0-41-m1-g:team-sale", 1, []string{"Hoa", "Minh"}), n.Packet.GetId())
}

func TestManyRecipientsArePaged(t *testing.T) {
	ns, err := builder.Notified(result(plan.Created, 0), snapshot(), recipients(2500, domain.Target{Kind: domain.TargetAll, At: sentAt}))
	noErr(t, err)
	equal(t, 3, len(ns))
	for i, n := range ns {
		d := n.Packet
		equal(t, "777-0-41-m0-all", d.GetNotification().GetId())
		equal(t, "", d.GetNotification().GetUser())
		equal(t, uint32(i+1), d.GetPart())
		equal(t, uint32(3), d.GetParts())
		equal(t, wspublish.PageID("777-0-41-m0-all", i+1, d.GetTargets().GetUsers()), d.GetId())
		equal(t, []int{1000, 1000, 500}[i], len(d.GetTargets().GetUsers()))
	}
	all := usersOf(ns)
	truth(t, slices.IsSorted(all) && len(all) == 2500 && len(slices.Compact(slices.Clone(all))) == 2500, "every user exactly once, sorted")
}

func TestDifferentTargetsFormSeparateGroups(t *testing.T) {
	all := domain.Target{Kind: domain.TargetAll, At: sentAt}
	lan := domain.Target{Kind: domain.TargetUser, ID: "Lan", At: sentAt}
	add := map[string]domain.Target{"Lan": lan, "Hoa": all, "Minh": all}
	ns, err := builder.Notified(result(plan.Created, 0), snapshot(), add)
	noErr(t, err)
	equal(t, 2, len(ns))
	byID := map[string][]string{}
	for _, n := range ns {
		byID[n.Packet.GetNotification().GetId()] = n.Packet.GetTargets().GetUsers()
	}
	equal(t, map[string][]string{"777-0-41-m0-all": {"Hoa", "Minh"}, "777-0-41-m0-u:Lan": {"Lan"}}, byID)
}

func TestNotifiedTargetKinds(t *testing.T) {
	cases := map[string]struct {
		target domain.Target
		id     string
		want   *chatimv1.MentionTarget
	}{
		"user":  {domain.Target{Kind: domain.TargetUser, ID: "Minh", At: sentAt}, "777-0-41-m0-u:Minh", &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "Minh", At: timestamppb.New(sentAt)}},
		"all":   {domain.Target{Kind: domain.TargetAll, At: sentAt}, "777-0-41-m0-all", &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_ALL, At: timestamppb.New(sentAt)}},
		"no at": {domain.Target{Kind: domain.TargetUser, ID: "Minh"}, "777-0-41-m0-u:Minh", &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "Minh"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ns, err := builder.Notified(result(plan.Created, 0), snapshot(), map[string]domain.Target{"Minh": c.target})
			noErr(t, err)
			truth(t, proto.Equal(c.want, ns[0].Packet.GetNotification().GetMentionNotified().GetTarget()))
			equal(t, c.id, ns[0].Packet.GetNotification().GetId())
		})
	}
}

func TestRetractedReasonByKindAndPaged(t *testing.T) {
	cases := map[plan.Kind]struct {
		reason chatimv1.RetractReason
		id     string
	}{
		plan.Edited:  {chatimv1.RetractReason_RETRACT_REASON_EDITED, "777-9-41-r3-edited"},
		plan.Deleted: {chatimv1.RetractReason_RETRACT_REASON_DELETED, "777-9-41-r3-deleted"},
	}
	for kind, c := range cases {
		r := result(kind, 3)
		r.Plan.Key.Thread = 9
		r.RoomType = domain.RoomDM
		ns, err := notify.Builder{Root: "notify", Page: 2}.Retracted(r, recipients(5, domain.Target{Kind: domain.TargetAll}))
		noErr(t, err)
		equal(t, 3, len(ns))
		for _, n := range ns {
			equal(t, notify.KindRetracted, n.Kind)
			equal(t, "notify.t1", n.Subject)
			nn := n.Packet.GetNotification()
			equal(t, c.id, nn.GetId())
			equal(t, c.reason, nn.GetMentionRetracted().GetReason())
			equal(t, chatimv1.RoomType_ROOM_TYPE_DM, nn.GetRoomType())
			equal(t, uint64(9), nn.GetThreadRoot())
			equal(t, uint32(3), nn.GetMessageVer())
			equal(t, "", nn.GetUser())
		}
		equal(t, 5, len(usersOf(ns)))
	}
}

func TestRerunKeepsUnchangedPagesAndRenamesChangedOnes(t *testing.T) {
	target := domain.Target{Kind: domain.TargetGroup, ID: "sale"}
	first, err := builder.Notified(result(plan.Created, 0), snapshot(), recipients(2500, target))
	noErr(t, err)
	again, err := builder.Notified(result(plan.Created, 0), snapshot(), recipients(2500, target))
	noErr(t, err)
	for i := range first {
		equal(t, first[i].Packet.GetId(), again[i].Packet.GetId())
	}
	left := recipients(2500, target)
	delete(left, "u2400")
	after, err := builder.Notified(result(plan.Created, 0), snapshot(), left)
	noErr(t, err)
	equal(t, first[0].Packet.GetId(), after[0].Packet.GetId())
	equal(t, first[1].Packet.GetId(), after[1].Packet.GetId())
	truth(t, first[2].Packet.GetId() != after[2].Packet.GetId(), "the changed page keeps its id")
	equal(t, 2499, len(usersOf(after)))
	equal(t, 499, len(after[2].Packet.GetTargets().GetUsers()))
}

func TestNoRecipientsBuildsNothing(t *testing.T) {
	ns, err := builder.Notified(result(plan.Created, 0), snapshot(), nil)
	noErr(t, err)
	equal(t, 0, len(ns))
	ns, err = builder.Retracted(result(plan.Created, 0), nil)
	noErr(t, err)
	equal(t, 0, len(ns))
}

func TestChannelRoomHasNoProtoType(t *testing.T) {
	r := result(plan.Created, 0)
	r.RoomType = domain.RoomChannel
	ns, err := builder.Notified(r, snapshot(), map[string]domain.Target{"Minh": {Kind: domain.TargetUser, ID: "Minh"}})
	noErr(t, err)
	equal(t, chatimv1.RoomType_ROOM_TYPE_UNSPECIFIED, ns[0].Packet.GetNotification().GetRoomType())
}

func TestBuildRejects(t *testing.T) {
	minh := map[string]domain.Target{"Minh": {Kind: domain.TargetUser, ID: "Minh"}}
	_, err := builder.Retracted(result(plan.Created, 0), minh)
	truth(t, errors.Is(err, apperr.ErrInvalidArgument) && errors.Is(err, plan.ErrInvalidEvent))
	_, err = builder.Retracted(result(plan.Edited, 1), map[string]domain.Target{"Minh.x": {}})
	truth(t, errors.Is(err, apperr.ErrInvalidArgument))
	r := result(plan.Edited, 1)
	r.Plan.Tenant = "T*"
	_, err = builder.Notified(r, snapshot(), minh)
	truth(t, errors.Is(err, apperr.ErrInvalidArgument))
	_, err = notify.Builder{Root: "bad root", Page: 10}.Notified(result(plan.Edited, 1), snapshot(), minh)
	truth(t, errors.Is(err, apperr.ErrInvalidArgument))
}
