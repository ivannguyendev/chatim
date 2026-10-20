package plan_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/plan"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var key = domain.MessageKey{Room: 777, Seq: 41}

func user(id string, at time.Time) domain.Target {
	return domain.Target{Kind: domain.TargetUser, ID: id, At: at}
}
func group(id string, at time.Time) domain.Target {
	return domain.Target{Kind: domain.TargetGroup, ID: id, At: at}
}
func all(at time.Time) domain.Target { return domain.Target{Kind: domain.TargetAll, At: at} }

func result(kind plan.Kind, ver uint32, at time.Time, current, previous, retired []domain.Target) plan.Result {
	return plan.Result{
		Plan:     domain.Plan{Key: key, Tenant: "t1", Sender: "Lan", Ver: ver, Live: current, Retired: retired},
		Current:  current,
		Previous: previous,
		RoomType: domain.RoomGroup,
		Sender:   "Lan",
		Ts:       at,
		Kind:     kind,
	}
}

func TestFromEvent(t *testing.T) {
	minhAt, opsAt := sentAt, sentAt
	withAll := message(0, userTarget("Minh", ts(minhAt)), groupTarget("team-sale", ts(opsAt)))
	withAll.MentionAll, withAll.MentionAllAt = true, ts(sentAt)
	legacy := message(0, userTarget("Minh", nil))
	legacy.MentionAll = true
	cases := map[string]struct {
		ev   *chatimv1.Event
		want plan.Result
	}{
		"created with a user, a group and @all": {
			ev: created(withAll),
			want: result(plan.Created, 0, sentAt,
				[]domain.Target{user("Minh", sentAt), group("team-sale", sentAt), all(sentAt)}, nil, nil),
		},
		"a target with no time takes the send time": {
			ev:   created(legacy),
			want: result(plan.Created, 0, sentAt, []domain.Target{user("Minh", sentAt), all(sentAt)}, nil, nil),
		},
		"edit adds Ha and drops Minh, which keeps its first time": {
			ev: edited(1, message(1, userTarget("Ha", ts(editedAt)), groupTarget("team-sale", ts(sentAt))),
				&chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{userTarget("Minh", ts(sentAt)), groupTarget("team-sale", ts(sentAt))}}),
			want: result(plan.Edited, 1, editedAt,
				[]domain.Target{user("Ha", editedAt), group("team-sale", sentAt)},
				[]domain.Target{user("Minh", sentAt), group("team-sale", sentAt)},
				[]domain.Target{user("Minh", sentAt)}),
		},
		"edit drops @all with no time": {
			ev:   edited(2, message(2), &chatimv1.MentionSet{All: true}),
			want: result(plan.Edited, 2, editedAt, nil, []domain.Target{all(sentAt)}, []domain.Target{all(sentAt)}),
		},
		"delete retires every previous target": {
			ev: deleted(3, &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{userTarget("Minh", ts(editedAt))}, All: true, AllAt: ts(sentAt)}),
			want: result(plan.Deleted, 3, editedAt, nil,
				[]domain.Target{user("Minh", editedAt), all(sentAt)},
				[]domain.Target{user("Minh", editedAt), all(sentAt)}),
		},
		"a replayed edit uses the snapshot version": {
			ev: edited(1, message(2, userTarget("Minh", ts(editedAt))), &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{userTarget("Ha", ts(sentAt))}}),
			want: result(plan.Edited, 2, editedAt, []domain.Target{user("Minh", editedAt)},
				[]domain.Target{user("Ha", sentAt)}, []domain.Target{user("Ha", sentAt)}),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := plan.FromEvent(c.ev)
			if err != nil {
				t.Fatalf("FromEvent: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("FromEvent =\n%+v\nwant\n%+v", got, c.want)
			}
		})
	}
}

func TestFromEventReadsEditOfADeletedSnapshotAsADelete(t *testing.T) {
	m := message(4)
	m.Deleted = true
	got, err := plan.FromEvent(edited(3, m, &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{userTarget("Minh", ts(sentAt))}}))
	if err != nil || got.Kind != plan.Deleted || got.Plan.Ver != 4 || len(got.Plan.Live) != 0 || len(got.Plan.Retired) != 1 {
		t.Fatalf("FromEvent = %+v, %v; want a delete at v4 retiring Minh", got, err)
	}
}

func TestFromEventMapsRoomTypes(t *testing.T) {
	cases := map[chatimv1.RoomType]domain.RoomType{
		chatimv1.RoomType_ROOM_TYPE_DM:          domain.RoomDM,
		chatimv1.RoomType_ROOM_TYPE_GROUP:       domain.RoomGroup,
		chatimv1.RoomType_ROOM_TYPE_UNSPECIFIED: domain.RoomChannel,
		chatimv1.RoomType(9):                    domain.RoomChannel,
	}
	for in, want := range cases {
		ev := created(message(0, userTarget("Minh", ts(sentAt))))
		ev.RoomType = in
		if got, err := plan.FromEvent(ev); err != nil || got.RoomType != want {
			t.Fatalf("room type %v = %v, %v; want %v", in, got.RoomType, err, want)
		}
	}
}

func TestFromEventRejects(t *testing.T) {
	cases := map[string]func(*chatimv1.Event){
		"no payload": func(ev *chatimv1.Event) { ev.Payload = nil },
		"another kind": func(ev *chatimv1.Event) {
			ev.Payload = &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{}}
		},
		"no message":   func(ev *chatimv1.Event) { ev.GetMessageCreated().Message = nil },
		"bad room":     func(ev *chatimv1.Event) { ev.RoomId = "x" },
		"room zero":    func(ev *chatimv1.Event) { ev.RoomId = "0" },
		"no time":      func(ev *chatimv1.Event) { ev.Ts = nil },
		"bad tenant":   func(ev *chatimv1.Event) { ev.Tenant = "" },
		"no sender":    func(ev *chatimv1.Event) { ev.GetMessageCreated().Message.Sender = "" },
		"seq zero":     func(ev *chatimv1.Event) { ev.Seq = 0 },
		"unknown kind": func(ev *chatimv1.Event) { ev.GetMessageCreated().Message.MentionTargets[0].Kind = 0 },
		"all as a target": func(ev *chatimv1.Event) {
			ev.GetMessageCreated().Message.MentionTargets[0].Kind = chatimv1.MentionKind_MENTION_KIND_ALL
		},
		"kind past all": func(ev *chatimv1.Event) { ev.GetMessageCreated().Message.MentionTargets[0].Kind = 4 },
		"bad target id": func(ev *chatimv1.Event) { ev.GetMessageCreated().Message.MentionTargets[0].Id = "a b" },
		"same target twice": func(ev *chatimv1.Event) {
			m := ev.GetMessageCreated().Message
			m.MentionTargets = append(m.MentionTargets, m.MentionTargets[0])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			ev := created(message(0, userTarget("Minh", ts(sentAt))))
			mutate(ev)
			_, err := plan.FromEvent(ev)
			if !errors.Is(err, plan.ErrInvalidEvent) || !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("FromEvent = %v, want ErrInvalidEvent", err)
			}
		})
	}
	if _, err := plan.FromEvent(nil); !errors.Is(err, plan.ErrInvalidEvent) {
		t.Fatalf("FromEvent(nil) = %v, want ErrInvalidEvent", err)
	}
}
