package e2e_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const bob = "e2e-bob"

func TestMemberIDsAndSubjects(t *testing.T) {
	at := time.UnixMilli(1700000000123)
	ids := map[string]string{
		e2e.RoomCreatedEventID(room):            "42-created",
		e2e.MemberEventID(room, bob, 3):         "42-mb-e2e-bob-v3",
		e2e.MemberCountEventID(room, 5):         "42-members-v5",
		e2e.ReadEventID(room, bob, 2):           "42-rd-e2e-bob-v2",
		e2e.HiddenEventID(room, bob, 0, 1):      "42-hd-e2e-bob-0-1",
		e2e.ClearedEventID(room, bob, at):       "42-cl-e2e-bob-1700000000123",
		e2e.LiveSubject("live", "t", room, "x"): "live.t.message.42.evt.x",
	}
	for got, want := range ids {
		if got != want {
			t.Fatalf("id = %q, want %q", got, want)
		}
	}
	classes := map[string]string{
		e2e.KindRoomCreated: "room", e2e.KindPinned: "room", e2e.KindUnpinned: "room", e2e.KindMemberCount: "room",
		e2e.KindMemberAdded: "member", e2e.KindMemberRemoved: "member", e2e.KindRoleChanged: "member", e2e.KindPriorityChanged: "member",
		e2e.KindRead: "member", e2e.KindHidden: "member", e2e.KindCleared: "member",
		e2e.KindCreated: "message", e2e.KindEdited: "message", e2e.KindDeleted: "message", e2e.KindReaction: "message", e2e.KindCounts: "message",
	}
	for kind, class := range classes {
		if got, want := e2e.LiveSubject("live", "e2e", room, kind), "live.e2e."+class+".42.evt."+kind; got != want {
			t.Fatalf("LiveSubject(%s) = %q, want %q", kind, got, want)
		}
	}
}

func envelope(id string) *chatimv1.Event { return &chatimv1.Event{Id: id, RoomId: room} }

func TestEventOfReadsMemberAndReadEvents(t *testing.T) {
	at := time.UnixMilli(1700000000123)
	owner, admin, member := chatimv1.MemberRole_MEMBER_ROLE_OWNER, chatimv1.MemberRole_MEMBER_ROLE_ADMIN, chatimv1.MemberRole_MEMBER_ROLE_MEMBER
	cases := []struct {
		payload func(*chatimv1.Event)
		want    e2e.Event
	}{
		{func(e *chatimv1.Event) {
			e.Payload = &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{Room: &chatimv1.Room{Id: room}}}
		}, e2e.Event{Kind: e2e.KindRoomCreated}},
		{func(e *chatimv1.Event) {
			e.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{User: bob, Role: member, Ver: 3, ReadSeq: 81, ReadVer: 1}}
		}, e2e.Event{Kind: e2e.KindMemberAdded, User: bob, Version: 3, Text: e2e.AddedPayload("member", 81, 1)}},
		{func(e *chatimv1.Event) {
			e.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{
				User: bob, Reason: chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT, PreviousRole: owner, Ver: 2,
			}}
		}, e2e.Event{Kind: e2e.KindMemberRemoved, User: bob, Version: 2, Text: "reason=left previous_role=owner"}},
		{func(e *chatimv1.Event) {
			e.Payload = &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{User: bob, Role: owner, PreviousRole: admin, Ver: 4}}
		}, e2e.Event{Kind: e2e.KindRoleChanged, User: bob, Version: 4, Text: e2e.RolePayload("owner", "admin")}},
		{func(e *chatimv1.Event) {
			e.Payload = &chatimv1.Event_MemberPriorityChanged{MemberPriorityChanged: &chatimv1.MemberPriorityChanged{User: bob, Priority: 5, Ver: 3}}
		}, e2e.Event{Kind: e2e.KindPriorityChanged, User: bob, Version: 3, Text: e2e.PriorityPayload(5, 0)}},
		{func(e *chatimv1.Event) {
			e.Payload = &chatimv1.Event_MemberCountChanged{MemberCountChanged: &chatimv1.MemberCountChanged{MemberCount: 3, MemberCountVer: 2}}
		}, e2e.Event{Kind: e2e.KindMemberCount, Text: e2e.CountPayload(3)}},
		{func(e *chatimv1.Event) {
			e.Payload = &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: bob, ReadSeq: 79, ReadVer: 2}}
		}, e2e.Event{Kind: e2e.KindRead, User: bob, Text: e2e.ReadPayload(79, 2)}},
		{func(e *chatimv1.Event) {
			e.Seq = 1
			e.Payload = &chatimv1.Event_MessageHidden{MessageHidden: &chatimv1.MessageHidden{User: bob, Seq: 1}}
		}, e2e.Event{Kind: e2e.KindHidden, User: bob, Seq: 1, Text: e2e.HiddenPayload(0, 1)}},
		{func(e *chatimv1.Event) {
			e.Payload = &chatimv1.Event_HistoryCleared{HistoryCleared: &chatimv1.HistoryCleared{User: bob, ClearedAt: timestamppb.New(at)}}
		}, e2e.Event{Kind: e2e.KindCleared, User: bob, Text: e2e.ClearedPayload(at)}},
	}
	for _, c := range cases {
		ev := envelope("id-" + c.want.Kind)
		c.payload(ev)
		want := c.want
		want.Room, want.ID, want.Subject = room, ev.GetId(), "s"
		got, ok := e2e.EventOf("s", ev)
		member := want.Kind != e2e.KindRoomCreated
		if !ok || got != want || got.IsMember() != member || got.IsMark() || got.IsChange() || got.IsCreated() {
			t.Fatalf("EventOf(%s) = %+v, %v; want %+v, true (member class %v)", want.Kind, got, ok, want, member)
		}
	}
	if e2e.AddedPayload("member", 81, 1) != "role=member read_seq=81 read_ver=1" || e2e.ClearedPayload(at) != "cleared_at=1700000000123" {
		t.Fatalf("payload formats = %q, %q", e2e.AddedPayload("member", 81, 1), e2e.ClearedPayload(at))
	}
}
