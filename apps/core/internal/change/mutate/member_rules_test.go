package mutate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func runMemberCommand(ctx context.Context, rg *rig, name string) error {
	var err error
	switch name {
	case "member adds":
		_, err = rg.m.AddMembers(ctx, add("mia", "r1", "nia"))
	case "admin adds":
		_, err = rg.m.AddMembers(ctx, add("ada", "r1", "nia"))
	case "member removes max":
		_, err = rg.m.RemoveMember(ctx, remove("mia", "max"))
	case "member removes zed":
		_, err = rg.m.RemoveMember(ctx, remove("mia", "zed"))
	case "member sets role of zed":
		_, err = rg.m.ChangeMemberRole(ctx, setRole("mia", "zed", domain.RoleAdmin))
	case "admin removes the owner":
		_, err = rg.m.RemoveMember(ctx, remove("ada", "owen"))
	case "admin sets a role":
		_, err = rg.m.ChangeMemberRole(ctx, setRole("ada", "mia", domain.RoleAdmin))
	case "admin sets a priority":
		_, err = rg.m.SetMemberPriority(ctx, setPriority("ada", "mia", 1))
	case "owner removes zed":
		_, err = rg.m.RemoveMember(ctx, remove("owen", "zed"))
	case "admin removes zed":
		_, err = rg.m.RemoveMember(ctx, remove("ada", "zed"))
	case "owner sets role of zed":
		_, err = rg.m.ChangeMemberRole(ctx, setRole("owen", "zed", domain.RoleAdmin))
	case "admin removes mia":
		_, err = rg.m.RemoveMember(ctx, remove("ada", "mia"))
	case "stranger adds":
		_, err = rg.m.AddMembers(ctx, add("zed", "r1", "nia"))
	}
	return err
}

func TestMemberCommandsFollowTheDefaultPolicy(t *testing.T) {
	for name, want := range map[string]error{
		"member adds":             access.ErrDenied,
		"member removes max":      access.ErrDenied,
		"member removes zed":      access.ErrDenied,
		"member sets role of zed": access.ErrDenied,
		"admin removes the owner": access.ErrDenied,
		"admin sets a role":       access.ErrDenied,
		"admin sets a priority":   access.ErrDenied,
		"stranger adds":           domain.ErrNotMember,
		"owner removes zed":       domain.ErrMemberNotFound,
		"admin removes zed":       domain.ErrMemberNotFound,
		"owner sets role of zed":  domain.ErrMemberNotFound,
		"admin adds":              nil,
		"admin removes mia":       nil,
	} {
		rg := newMemberRig(t, nil)
		if err := runMemberCommand(t.Context(), rg, name); !errors.Is(err, want) || (want == nil) != (err == nil) {
			t.Fatalf("%s = %v, want %v", name, err, want)
		}
	}
}

func TestMemberCommandsAskThePolicyWithCallerTargetAndRole(t *testing.T) {
	var asked []access.Request
	record := access.PolicyFunc(func(_ context.Context, r access.Request) error {
		asked = append(asked, r)
		return access.ErrDenied
	})
	rg := newMemberRig(t, record)
	for _, run := range []func() error{
		func() error {
			_, err := rg.m.ChangeMemberRole(t.Context(), setRole("mia", "ada", domain.RoleOwner))
			return err
		},
		func() error { _, err := rg.m.RemoveMember(t.Context(), remove("ada", "zed")); return err },
		func() error { _, err := rg.m.SetMemberPriority(t.Context(), setPriority("owen", "max", 4)); return err },
	} {
		if err := run(); !errors.Is(err, access.ErrDenied) {
			t.Fatalf("command = %v, want the policy's ErrDenied", err)
		}
	}
	if len(asked) != 3 {
		t.Fatalf("policy asked %d times, want 3", len(asked))
	}
	first, second, third := asked[0], asked[1], asked[2]
	switch {
	case first.Action != access.ChangeMemberRole || first.Member.User != "mia" || first.Target != rg.doc(t, "ada") || first.Role != domain.RoleOwner:
		t.Fatalf("role request = %+v", first)
	case second.Action != access.RemoveMember || second.Member.User != "ada" || second.Target.User != "zed" || second.Target.Role != domain.RoleMember || second.Role != "":
		t.Fatalf("remove request = %+v, want a stand-in member target", second)
	case third.Action != access.SetMemberPriority || third.Member.Role != domain.RoleOwner || third.Target.User != "max" || third.Room.ID != group:
		t.Fatalf("priority request = %+v", third)
	}
}

func TestDirectRoomsKeepTheirTwoMembers(t *testing.T) {
	rg := newMemberRig(t, nil)
	r, members, err := domain.NewRoom(tenant, "owen", domain.RoomDM, "", []string{"owen", "mia"}, created, direct)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create dm: %v", err)
	}
	var errs []error
	_, err = rg.m.AddMembers(t.Context(), mutate.AddMembersCmd{Tenant: tenant, User: "owen", Room: direct, Users: []string{"nia"}, RequestID: "r1"})
	errs = append(errs, err)
	_, err = rg.m.RemoveMember(t.Context(), mutate.RemoveMemberCmd{Tenant: tenant, User: "owen", Room: direct, Target: "mia"})
	errs = append(errs, err)
	_, err = rg.m.LeaveRoom(t.Context(), mutate.LeaveRoomCmd{Tenant: tenant, User: "mia", Room: direct})
	errs = append(errs, err)
	_, err = rg.m.ChangeMemberRole(t.Context(), mutate.ChangeRoleCmd{Tenant: tenant, User: "owen", Room: direct, Target: "mia", Role: domain.RoleAdmin})
	errs = append(errs, err)
	_, err = rg.m.SetMemberPriority(t.Context(), mutate.SetPriorityCmd{Tenant: tenant, User: "owen", Room: direct, Target: "mia", Priority: 1})
	errs = append(errs, err)
	for i, err := range errs {
		if !errors.Is(err, domain.ErrDirectRoom) || !errors.Is(err, apperr.ErrFailedPrecondition) {
			t.Fatalf("dm command %d = %v, want ErrDirectRoom", i, err)
		}
	}
	if calls := rg.calls.list(); len(calls) != 0 {
		t.Fatalf("calls = %v, want nothing touched", calls)
	}
}

func TestMemberCommandsRejectBadInputFirst(t *testing.T) {
	rg := newMemberRig(t, nil)
	ctx := t.Context()
	nowhere := func(c mutate.AddMembersCmd) mutate.AddMembersCmd { c.Room = 99; return c }
	checks := map[string]func() error{
		"bad request id": func() error { _, err := rg.m.AddMembers(ctx, nowhere(add("owen", "bad id", "nia"))); return err },
		"no request id":  func() error { _, err := rg.m.AddMembers(ctx, nowhere(add("owen", "", "nia"))); return err },
		"no users":       func() error { _, err := rg.m.AddMembers(ctx, nowhere(add("owen", "r1"))); return err },
		"bad user":       func() error { _, err := rg.m.AddMembers(ctx, nowhere(add("owen", "r1", "nia", "a b"))); return err },
		"remove self":    func() error { _, err := rg.m.RemoveMember(ctx, remove("owen", "owen")); return err },
		"bad target":     func() error { _, err := rg.m.RemoveMember(ctx, remove("owen", "")); return err },
		"bad role":       func() error { _, err := rg.m.ChangeMemberRole(ctx, setRole("owen", "mia", "boss")); return err },
		"empty role":     func() error { _, err := rg.m.ChangeMemberRole(ctx, setRole("owen", "mia", "")); return err },
		"bad pri target": func() error { _, err := rg.m.SetMemberPriority(ctx, setPriority("owen", "a b", 1)); return err },
	}
	for name, run := range checks {
		if err := run(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s = %v, want ErrInvalidArgument", name, err)
		}
	}
	if calls := rg.calls.list(); len(calls) != 0 {
		t.Fatalf("calls = %v, want nothing touched", calls)
	}
}

func TestTheLastOwnerCannotStepDown(t *testing.T) {
	rg := newMemberRig(t, nil)
	if _, err := rg.m.ChangeMemberRole(t.Context(), setRole("owen", "owen", domain.RoleAdmin)); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("last owner steps down = %v, want ErrLastOwner", err)
	}
	if _, err := rg.m.ChangeMemberRole(t.Context(), setRole("owen", "ada", domain.RoleOwner)); err != nil {
		t.Fatalf("promote ada: %v", err)
	}
	res, err := rg.m.ChangeMemberRole(t.Context(), setRole("owen", "owen", domain.RoleAdmin))
	if err != nil || res.Member.Role != domain.RoleAdmin || res.Successor != "" {
		t.Fatalf("owner steps down beside another owner = %+v, %v", res, err)
	}
	if ev := rg.memberEvents(); len(ev) != 2 || ev[0] != "role ada" || ev[1] != "role owen" {
		t.Fatalf("events = %v", ev)
	}
}
