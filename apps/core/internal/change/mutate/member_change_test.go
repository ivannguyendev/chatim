package mutate_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRemoveMemberLeavesATombstoneAndRepeatsAsANoOp(t *testing.T) {
	rg := newMemberRig(t, nil)
	res, err := rg.m.RemoveMember(t.Context(), remove("owen", "mia"))
	if err != nil || !res.Changed || res.Member.State != domain.MemberRemoved || res.Member.Ver != 2 || res.PreviousRole != domain.RoleMember {
		t.Fatalf("RemoveMember = %+v, %v; want mia removed at ver 2", res, err)
	}
	_, events := rg.events.list()
	if ev := rg.memberEvents(); !slices.Equal(ev, []string{"removed mia", "count 3"}) ||
		events[0].GetMemberRemoved().GetReason() != chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED {
		t.Fatalf("events = %v, want mia removed then the count", ev)
	}
	again, err := rg.m.RemoveMember(t.Context(), remove("owen", "mia"))
	if err != nil || again.Changed || again.Member != rg.doc(t, "mia") || len(rg.memberEvents()) != 2 {
		t.Fatalf("second RemoveMember = %+v, %v; want a no-op", again, err)
	}
	rg.seedRole(t, "max", domain.RoleAdmin)
	if _, err := rg.m.LeaveRoom(t.Context(), leave("max")); err != nil {
		t.Fatalf("max leaves: %v", err)
	}
	if res, err := rg.m.RemoveMember(t.Context(), remove("ada", "max")); err != nil || res.Changed {
		t.Fatalf("admin removes a former admin who left = %+v, %v; want a no-op", res, err)
	}
}

func TestLeaveRoomIsDesiredState(t *testing.T) {
	rg := newMemberRig(t, nil)
	res, err := rg.m.LeaveRoom(t.Context(), leave("mia"))
	if err != nil || !res.Changed || res.Member.Active() || res.Member.UpdatedBy != "mia" {
		t.Fatalf("LeaveRoom = %+v, %v", res, err)
	}
	if _, events := rg.events.list(); events[0].GetMemberRemoved().GetReason() != chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT {
		t.Fatalf("leave event = %v, want reason LEFT", events[0])
	}
	again, err := rg.m.LeaveRoom(t.Context(), leave("mia"))
	if err != nil || again.Changed || again.Member != rg.doc(t, "mia") {
		t.Fatalf("leaving again = %+v, %v; want a no-op", again, err)
	}
	if _, err := rg.m.LeaveRoom(t.Context(), leave("zed")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("a stranger leaves = %v, want ErrNotMember", err)
	}
	if ev := rg.memberEvents(); !slices.Equal(ev, []string{"removed mia", "count 3"}) {
		t.Fatalf("events = %v", ev)
	}
}

func TestChangeMemberRoleIsDesiredState(t *testing.T) {
	rg := newMemberRig(t, nil)
	res, err := rg.m.ChangeMemberRole(t.Context(), setRole("owen", "mia", domain.RoleAdmin))
	if err != nil || !res.Changed || res.Member.Role != domain.RoleAdmin || res.PreviousRole != domain.RoleMember || res.Member.Ver != 2 {
		t.Fatalf("ChangeMemberRole = %+v, %v", res, err)
	}
	again, err := rg.m.ChangeMemberRole(t.Context(), setRole("owen", "mia", domain.RoleAdmin))
	if err != nil || again.Changed || again.PreviousRole != domain.RoleAdmin || again.Member.Ver != 2 {
		t.Fatalf("same role again = %+v, %v; want a no-op", again, err)
	}
	if _, err := rg.m.LeaveRoom(t.Context(), leave("max")); err != nil {
		t.Fatalf("max leaves: %v", err)
	}
	if _, err := rg.m.ChangeMemberRole(t.Context(), setRole("owen", "max", domain.RoleAdmin)); !errors.Is(err, domain.ErrMemberNotFound) {
		t.Fatalf("role of someone who left = %v, want ErrMemberNotFound", err)
	}
	if ev := rg.memberEvents(); !slices.Equal(ev, []string{"role mia", "removed max", "count 3"}) {
		t.Fatalf("events = %v", ev)
	}
	if calls := rg.calls.list(); calls[0] != "apply_member" {
		t.Fatalf("calls = %v, want no timer for a role change", calls)
	}
}

func TestSetMemberPriorityIsOwnerOnlyAndDesiredState(t *testing.T) {
	rg := newMemberRig(t, nil)
	res, err := rg.m.SetMemberPriority(t.Context(), setPriority("owen", "mia", 7))
	if err != nil || !res.Changed || res.Member.Priority != 7 || res.PreviousPriority != 0 || res.Member.Ver != 2 || res.Member.Role != domain.RoleMember {
		t.Fatalf("SetMemberPriority = %+v, %v", res, err)
	}
	if ev := rg.memberEvents(); !slices.Equal(ev, []string{"priority mia"}) {
		t.Fatalf("events = %v, want one member_priority_changed", ev)
	}
	if _, err := rg.m.SetMemberPriority(t.Context(), setPriority("ada", "mia", 9)); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("admin sets priority = %v, want ErrDenied", err)
	}
	again, err := rg.m.SetMemberPriority(t.Context(), setPriority("owen", "mia", 7))
	if err != nil || again.Changed || again.PreviousPriority != 7 {
		t.Fatalf("same priority = %+v, %v; want a no-op", again, err)
	}
	if res, err := rg.m.SetMemberPriority(t.Context(), setPriority("owen", "owen", -3)); err != nil || res.Member.Role != domain.RoleOwner {
		t.Fatalf("owner sets own priority = %+v, %v", res, err)
	}
	if c := rg.memberCount(t); c != (domain.MemberCount{Count: 4, Ver: 1}) || slices.Contains(rg.calls.list(), "arm") {
		t.Fatalf("count %+v, calls %v; want priority to leave the count alone", c, rg.calls.list())
	}
}

func TestOwnerChangesGoThroughTheOwnerTransaction(t *testing.T) {
	rg := newMemberRig(t, nil)
	res, err := rg.m.LeaveRoom(t.Context(), leave("owen"))
	if err != nil || !res.Changed || res.Successor != "ada" || res.Member.Active() || res.PreviousRole != domain.RoleOwner {
		t.Fatalf("last owner leaves = %+v, %v; want ada as successor", res, err)
	}
	if heir := rg.doc(t, "ada"); heir.Role != domain.RoleOwner || heir.UpdatedBy != "owen" {
		t.Fatalf("ada = %+v, want promoted to owner", heir)
	}
	if ev := rg.memberEvents(); !slices.Equal(ev, []string{"role ada", "removed owen", "count 3"}) {
		t.Fatalf("events = %v, want the successor first, then the leaver and the count", ev)
	}
	if c := rg.memberCount(t); c != (domain.MemberCount{Count: 3, Ver: 2}) {
		t.Fatalf("count = %+v, want 3 at ver 2 from the transaction", c)
	}
	if calls := rg.calls.list(); !slices.Equal(calls, []string{"change_owners"}) || !slices.Equal(rg.forgets.list(), []uint64{group}) {
		t.Fatalf("calls = %v, forgets %v; want only the owner transaction", calls, rg.forgets.list())
	}
}

func TestRemovingAnActiveMemberLowersTheCountOnce(t *testing.T) {
	rg := newMemberRig(t, nil)
	for range 2 {
		if _, err := rg.m.RemoveMember(t.Context(), remove("ada", "mia")); err != nil {
			t.Fatalf("RemoveMember: %v", err)
		}
	}
	if _, err := rg.m.ChangeMemberRole(t.Context(), setRole("owen", "max", domain.RoleAdmin)); err != nil {
		t.Fatalf("ChangeMemberRole: %v", err)
	}
	want := []string{"arm", "apply_member", "add_member_count", "disarm", "apply_member"}
	if calls := rg.calls.list(); !slices.Equal(calls, want) || rg.memberCount(t).Count != 3 {
		t.Fatalf("calls = %v, count %+v; want %v", calls, rg.memberCount(t), want)
	}
}
