package access_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func memberRequest(action access.Action, caller, target domain.Role) access.Request {
	return access.Request{
		Action: action,
		User:   "caller",
		Member: domain.Member{User: "caller", Role: caller, State: domain.MemberActive},
		Target: domain.Member{User: "target", Role: target},
		Role:   domain.RoleAdmin,
	}
}

func TestMemberActionNames(t *testing.T) {
	names := map[access.Action]string{
		access.AddMembers:        "add_members",
		access.RemoveMember:      "remove_member",
		access.LeaveRoom:         "leave_room",
		access.ChangeMemberRole:  "change_member_role",
		access.SetMemberPriority: "set_member_priority",
		access.MarkRead:          "mark_read",
	}
	for action, name := range names {
		if string(action) != name {
			t.Fatalf("action %q, want %q", action, name)
		}
	}
}

func TestDefaultPolicyMemberRules(t *testing.T) {
	owner, admin, member := domain.RoleOwner, domain.RoleAdmin, domain.RoleMember
	cases := []struct {
		action access.Action
		caller domain.Role
		target domain.Role
		want   error
	}{
		{access.AddMembers, owner, member, nil},
		{access.AddMembers, admin, member, nil},
		{access.AddMembers, member, member, access.ErrDenied},
		{access.RemoveMember, owner, owner, nil},
		{access.RemoveMember, owner, admin, nil},
		{access.RemoveMember, owner, member, nil},
		{access.RemoveMember, admin, owner, access.ErrDenied},
		{access.RemoveMember, admin, admin, access.ErrDenied},
		{access.RemoveMember, admin, member, nil},
		{access.RemoveMember, member, owner, access.ErrDenied},
		{access.RemoveMember, member, admin, access.ErrDenied},
		{access.RemoveMember, member, member, access.ErrDenied},
		{access.ChangeMemberRole, owner, member, nil},
		{access.ChangeMemberRole, owner, admin, nil},
		{access.ChangeMemberRole, admin, member, access.ErrDenied},
		{access.ChangeMemberRole, member, member, access.ErrDenied},
		{access.SetMemberPriority, owner, member, nil},
		{access.SetMemberPriority, admin, member, access.ErrDenied},
		{access.SetMemberPriority, member, member, access.ErrDenied},
		{access.LeaveRoom, owner, owner, nil},
		{access.LeaveRoom, admin, admin, nil},
		{access.LeaveRoom, member, member, nil},
		{access.MarkRead, owner, owner, nil},
		{access.MarkRead, admin, admin, nil},
		{access.MarkRead, member, member, nil},
	}
	policies := map[string]access.DefaultPolicy{
		"nothing locked": {},
		"text locked":    {LockedKinds: []domain.Kind{domain.KindText}},
	}
	for label, p := range policies {
		for _, tc := range cases {
			err := p.Check(t.Context(), memberRequest(tc.action, tc.caller, tc.target))
			if !errors.Is(err, tc.want) {
				t.Fatalf("%s: %s by %s on %s = %v, want %v", label, tc.action, tc.caller, tc.target, err, tc.want)
			}
		}
	}
}

func TestDefaultPolicyKeepsItsMessageRulesNextToMemberRules(t *testing.T) {
	owner := domain.Member{User: "alice", Role: domain.RoleOwner, State: domain.MemberActive}
	cases := []struct {
		name   string
		policy access.DefaultPolicy
		action access.Action
		author string
		want   error
	}{
		{"owner edits another's message", access.DefaultPolicy{}, access.EditMessage, "bob", access.ErrDenied},
		{"owner deletes another's message", access.DefaultPolicy{}, access.DeleteMessage, "bob", access.ErrDenied},
		{"owner edits own message", access.DefaultPolicy{}, access.EditMessage, "alice", nil},
		{"owner deletes own locked message", access.DefaultPolicy{LockedKinds: []domain.Kind{domain.KindText}}, access.DeleteMessage, "alice", access.ErrDenied},
		{"owner hides another's message", access.DefaultPolicy{}, access.HideMessage, "bob", nil},
	}
	for _, tc := range cases {
		req := access.Request{Action: tc.action, User: "alice", Author: tc.author, Kind: domain.KindText, Member: owner, Target: owner, Role: domain.RoleOwner}
		if err := tc.policy.Check(t.Context(), req); !errors.Is(err, tc.want) {
			t.Fatalf("%s = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestAllowMembersAllowsMemberActions(t *testing.T) {
	actions := []access.Action{
		access.AddMembers, access.RemoveMember, access.LeaveRoom,
		access.ChangeMemberRole, access.SetMemberPriority, access.MarkRead,
	}
	for _, a := range actions {
		req := memberRequest(a, domain.RoleMember, domain.RoleOwner)
		if err := (access.AllowMembers{}).Check(t.Context(), req); err != nil {
			t.Fatalf("AllowMembers %s by a member on the owner = %v, want nil", a, err)
		}
	}
}
