package access

import (
	"context"
	"fmt"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Action string

const (
	ReadHistory     Action = "read_history"
	SendMessage     Action = "send_message"
	EditMessage     Action = "edit_message"
	DeleteMessage   Action = "delete_message"
	HideMessage     Action = "hide_message"
	ClearHistory    Action = "clear_history"
	ReadEditHistory Action = "read_edit_history"
	ReactMessage    Action = "react_message"
	PinMessage      Action = "pin_message"
	UnpinMessage    Action = "unpin_message"

	AddMembers        Action = "add_members"
	RemoveMember      Action = "remove_member"
	LeaveRoom         Action = "leave_room"
	ChangeMemberRole  Action = "change_member_role"
	SetMemberPriority Action = "set_member_priority"
	MarkRead          Action = "mark_read"
)

var ErrDenied = fmt.Errorf("action denied: %w", apperr.ErrPermissionDenied)

type Request struct {
	Action Action
	User   string
	Author string
	Kind   domain.Kind
	Room   domain.Room
	Member domain.Member
	Target domain.Member
	Role   domain.Role
}

type Policy interface {
	Check(ctx context.Context, req Request) error
}

type PolicyFunc func(ctx context.Context, req Request) error

func (f PolicyFunc) Check(ctx context.Context, req Request) error { return f(ctx, req) }

type AllowMembers struct{}

func (AllowMembers) Check(context.Context, Request) error { return nil }

type DefaultPolicy struct {
	LockedKinds []domain.Kind
}

func (p DefaultPolicy) Check(_ context.Context, req Request) error {
	switch req.Action {
	case EditMessage, DeleteMessage:
		return denyUnless(!slices.Contains(p.LockedKinds, req.Kind) && req.Author == req.User)
	case AddMembers:
		return denyUnless(req.Member.Role == domain.RoleOwner || req.Member.Role == domain.RoleAdmin)
	case RemoveMember:
		return denyUnless(req.Member.Role == domain.RoleOwner ||
			(req.Member.Role == domain.RoleAdmin && req.Target.Role == domain.RoleMember))
	case ChangeMemberRole, SetMemberPriority:
		return denyUnless(req.Member.Role == domain.RoleOwner)
	default:
		return nil
	}
}

func denyUnless(allowed bool) error {
	if allowed {
		return nil
	}
	return ErrDenied
}
