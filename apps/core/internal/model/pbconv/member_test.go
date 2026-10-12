package pbconv_test

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var changedAt = sentAt.Add(5 * time.Minute)

func activeMember() domain.Member {
	return domain.Member{
		Room: 9_007_199_254_740_993, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: sentAt,
		State: domain.MemberActive, Ver: 4, PreviousRole: domain.RoleMember, PreviousState: domain.MemberActive,
		RequestID: "req-1", UpdatedAt: changedAt, UpdatedBy: "alice", LastChangeAt: changedAt, ReadSeq: 12, ReadVer: 3,
	}
}

func memberEnvelope(id, actor string) *chatimv1.Event {
	return &chatimv1.Event{
		Id: id, Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Actor: actor, Ts: timestamppb.New(changedAt),
	}
}

func TestMemberAddedIsOneMemberEventWithTheReadPosition(t *testing.T) {
	m := activeMember()
	m.PreviousState = domain.MemberRemoved
	want := memberEnvelope("9007199254740993-mb-bob-v4", "alice")
	want.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{
		User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_MEMBER, JoinedAt: timestamppb.New(sentAt),
		Ver: 4, RequestId: "req-1", ReadSeq: 12, ReadVer: 3,
	}}
	if got := pbconv.MemberEvent(domain.RoomGroup, m); !proto.Equal(got, want) {
		t.Fatalf("MemberEvent(rejoin) = %v, want %v", got, want)
	}
}

func TestMembersCreatedWithTheRoomAreAnnouncedToo(t *testing.T) {
	_, members, err := domain.NewRoom("acme", "alice", domain.RoomGroup, "g", []string{"alice", "bob"}, changedAt, 9_007_199_254_740_993)
	if err != nil || len(members) != 2 {
		t.Fatalf("NewRoom = %d members, %v, want 2", len(members), err)
	}
	for _, m := range members {
		ev := pbconv.MemberEvent(domain.RoomGroup, m)
		added := ev.GetMemberAdded()
		if added == nil || ev.GetId() != pbconv.MemberEventID(m.Room, m.User, 1) || added.GetUser() != m.User || added.GetRole() != pbconv.MemberRole(m.Role) {
			t.Fatalf("MemberEvent(%s created with the room) = %v, want member_added v1", m.User, ev)
		}
	}
}

func TestMemberRemovedSaysWhetherTheUserLeft(t *testing.T) {
	for name, tc := range map[string]struct {
		by     string
		reason chatimv1.MemberRemovedReason
	}{
		"removed": {"alice", chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED},
		"left":    {"bob", chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT},
	} {
		m := activeMember().Next(domain.RoleAdmin, domain.MemberRemoved, 0, "req-2", tc.by, changedAt)
		m.PreviousRole = domain.RoleAdmin
		want := memberEnvelope("9007199254740993-mb-bob-v5", tc.by)
		want.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{
			User: "bob", Reason: tc.reason, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_ADMIN, Ver: 5, RequestId: "req-2",
		}}
		if got := pbconv.MemberEvent(domain.RoomGroup, m); !proto.Equal(got, want) {
			t.Fatalf("%s: MemberEvent = %v, want %v", name, got, want)
		}
	}
}

func TestMemberRoleChangedCarriesBothRolesAndOtherDocsCarryNothing(t *testing.T) {
	m := activeMember().Next(domain.RoleAdmin, domain.MemberActive, 0, "req-3", "alice", changedAt)
	want := memberEnvelope("9007199254740993-mb-bob-v5", "alice")
	want.Payload = &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{
		User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_MEMBER, Ver: 5, RequestId: "req-3",
	}}
	if got := pbconv.MemberEvent(domain.RoomGroup, m); !proto.Equal(got, want) {
		t.Fatalf("MemberEvent(role change) = %v, want %v", got, want)
	}
	removed := activeMember()
	removed.State, removed.PreviousState = domain.MemberRemoved, domain.MemberRemoved
	for name, m := range map[string]domain.Member{"unchanged active": activeMember(), "removed twice": removed, "zero": {}} {
		if got := pbconv.MemberEvent(domain.RoomGroup, m); got != nil {
			t.Fatalf("MemberEvent(%s) = %v, want nil", name, got)
		}
	}
}

func TestMemberPriorityChangedCarriesBothPriorities(t *testing.T) {
	m := activeMember().Next(domain.RoleMember, domain.MemberActive, 7, "req-4", "bob", changedAt)
	m.PreviousPriority = -2
	want := memberEnvelope("9007199254740993-mb-bob-v5", "bob")
	want.Payload = &chatimv1.Event_MemberPriorityChanged{MemberPriorityChanged: &chatimv1.MemberPriorityChanged{
		User: "bob", Priority: 7, PreviousPriority: -2, Ver: 5, RequestId: "req-4",
	}}
	if got := pbconv.MemberEvent(domain.RoomGroup, m); !proto.Equal(got, want) {
		t.Fatalf("MemberEvent(priority change) = %v, want %v", got, want)
	}
}

func TestMemberRolesMapBothWays(t *testing.T) {
	pairs := map[domain.Role]chatimv1.MemberRole{
		domain.RoleOwner:  chatimv1.MemberRole_MEMBER_ROLE_OWNER,
		domain.RoleAdmin:  chatimv1.MemberRole_MEMBER_ROLE_ADMIN,
		domain.RoleMember: chatimv1.MemberRole_MEMBER_ROLE_MEMBER,
	}
	for role, pb := range pairs {
		if got := pbconv.MemberRole(role); got != pb {
			t.Errorf("MemberRole(%s) = %v, want %v", role, got, pb)
		}
		if got, err := pbconv.DomainMemberRole(pb); err != nil || got != role {
			t.Errorf("DomainMemberRole(%v) = %q, %v, want %q", pb, got, err, role)
		}
	}
	if got := pbconv.MemberRole("guest"); got != chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED {
		t.Errorf("MemberRole(guest) = %v, want UNSPECIFIED", got)
	}
	for _, pb := range []chatimv1.MemberRole{chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED, chatimv1.MemberRole(99)} {
		if _, err := pbconv.DomainMemberRole(pb); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("DomainMemberRole(%v) error = %v, want ErrInvalidArgument", pb, err)
		}
	}
}
