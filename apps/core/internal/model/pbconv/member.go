package pbconv

import (
	"fmt"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errUnknownMemberRole = fmt.Errorf("%w: role", apperr.ErrInvalidArgument)

func MemberEventID(room uint64, user string, ver uint32) string {
	return RoomID(room) + "-mb-" + user + "-v" + strconv.FormatUint(uint64(ver), 10)
}

func MemberRole(r domain.Role) chatimv1.MemberRole {
	switch r {
	case domain.RoleOwner:
		return chatimv1.MemberRole_MEMBER_ROLE_OWNER
	case domain.RoleAdmin:
		return chatimv1.MemberRole_MEMBER_ROLE_ADMIN
	case domain.RoleMember:
		return chatimv1.MemberRole_MEMBER_ROLE_MEMBER
	default:
		return chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED
	}
}

func DomainMemberRole(r chatimv1.MemberRole) (domain.Role, error) {
	switch r {
	case chatimv1.MemberRole_MEMBER_ROLE_OWNER:
		return domain.RoleOwner, nil
	case chatimv1.MemberRole_MEMBER_ROLE_ADMIN:
		return domain.RoleAdmin, nil
	case chatimv1.MemberRole_MEMBER_ROLE_MEMBER:
		return domain.RoleMember, nil
	default:
		return "", errUnknownMemberRole
	}
}

func MemberEvent(roomType domain.RoomType, m domain.Member) *chatimv1.Event {
	ev := roomEnvelope(m.Room, m.Tenant, roomType, MemberEventID(m.Room, m.User, m.Ver), m.UpdatedBy, m.UpdatedAt)
	wasActive := m.PreviousState == domain.MemberActive
	switch {
	case m.Active() && !wasActive:
		ev.Payload = memberAdded(m)
	case m.State == domain.MemberRemoved && wasActive:
		ev.Payload = memberRemoved(m)
	case m.Active() && m.Role != m.PreviousRole:
		ev.Payload = &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{
			User: m.User, Role: MemberRole(m.Role), PreviousRole: MemberRole(m.PreviousRole), Ver: m.Ver, RequestId: m.RequestID,
		}}
	case m.Active() && m.Priority != m.PreviousPriority:
		ev.Payload = &chatimv1.Event_MemberPriorityChanged{MemberPriorityChanged: &chatimv1.MemberPriorityChanged{
			User: m.User, Priority: m.Priority, PreviousPriority: m.PreviousPriority, Ver: m.Ver, RequestId: m.RequestID,
		}}
	default:
		return nil
	}
	return ev
}

func memberAdded(m domain.Member) *chatimv1.Event_MemberAdded {
	return &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{
		User: m.User, Role: MemberRole(m.Role), JoinedAt: timestamppb.New(m.JoinedAt),
		Ver: m.Ver, RequestId: m.RequestID, ReadSeq: m.ReadSeq, ReadVer: m.ReadVer,
	}}
}

func memberRemoved(m domain.Member) *chatimv1.Event_MemberRemoved {
	reason := chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED
	if m.UpdatedBy == m.User {
		reason = chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT
	}
	return &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{
		User: m.User, Reason: reason, PreviousRole: MemberRole(m.PreviousRole), Ver: m.Ver, RequestId: m.RequestID,
	}}
}

func roomEnvelope(room uint64, tenant string, roomType domain.RoomType, id, actor string, at time.Time) *chatimv1.Event {
	return &chatimv1.Event{
		Id: id, Tenant: tenant, RoomId: RoomID(room), RoomType: RoomType(roomType), Actor: actor, Ts: timestamppb.New(at),
	}
}
