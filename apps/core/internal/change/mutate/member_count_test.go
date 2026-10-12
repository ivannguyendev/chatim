package mutate_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestAddMembersRaisesTheCountByTheDocsItChanged(t *testing.T) {
	rg := newMemberRig(t, nil)
	got, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia", "mia", "kai"))
	if err != nil || !slices.Equal(users(got), []string{"nia", "kai"}) {
		t.Fatalf("AddMembers = %v, %v", users(got), err)
	}
	if c := rg.memberCount(t); c != (domain.MemberCount{Count: 6, Ver: 2}) {
		t.Fatalf("count = %+v, want 6 at ver 2", c)
	}
	_, events := rg.events.list()
	if ev := rg.memberEvents(); !slices.Equal(ev, []string{"added nia", "added kai", "count 6"}) || events[2].GetMemberCountChanged().GetMemberCountVer() != 2 {
		t.Fatalf("events = %v, want the members then the count at ver 2", ev)
	}
	for _, retry := range []string{"r1", "r2"} {
		if _, err := rg.m.AddMembers(t.Context(), add("owen", retry, "nia", "mia", "kai")); err != nil {
			t.Fatalf("AddMembers %s again: %v", retry, err)
		}
	}
	counted := 0
	for _, c := range rg.calls.list() {
		if c == "add_member_count" {
			counted++
		}
	}
	if counted != 1 || rg.memberCount(t).Count != 6 || len(rg.memberEvents()) != 3 {
		t.Fatalf("count increments = %d, count %+v, events %v; want one increment", counted, rg.memberCount(t), rg.memberEvents())
	}
}

func TestAFailedCountIncrementStillSucceedsAndLeavesTheTimerArmed(t *testing.T) {
	rg := newMemberRig(t, nil)
	rg.members.countErr = errBoom
	got, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia"))
	if err != nil || len(got) != 1 {
		t.Fatalf("AddMembers with a failed count = %v, %v; want success", users(got), err)
	}
	if ev := rg.memberEvents(); !slices.Equal(ev, []string{"added nia"}) || rg.timers.pending() != 1 {
		t.Fatalf("events %v, pending timers %d; want no count event and the timer armed", ev, rg.timers.pending())
	}
	res, err := rg.m.RemoveMember(t.Context(), remove("owen", "mia"))
	if err != nil || !res.Changed || rg.timers.pending() != 2 {
		t.Fatalf("RemoveMember with a failed count = %+v, %v, pending %d", res, err, rg.timers.pending())
	}
	if c := rg.memberCount(t); c != (domain.MemberCount{Count: 4, Ver: 1}) {
		t.Fatalf("count = %+v, want it untouched for the timer to repair", c)
	}
}

func TestTheTimerIsArmedBeforeTheMemberWriteAndDisarmedAfterTheCount(t *testing.T) {
	rg := newMemberRig(t, nil)
	if _, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia")); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	if _, err := rg.m.LeaveRoom(t.Context(), leave("mia")); err != nil {
		t.Fatalf("LeaveRoom: %v", err)
	}
	want := []string{"arm", "add_members", "add_member_count", "disarm", "arm", "apply_member", "add_member_count", "disarm"}
	if calls := rg.calls.list(); !slices.Equal(calls, want) || rg.timers.pending() != 0 {
		t.Fatalf("calls = %v, pending %d; want %v", calls, rg.timers.pending(), want)
	}
}

func TestAFailedArmWritesNothingAndIsRetryLater(t *testing.T) {
	rg := newMemberRig(t, nil)
	rg.timers.err = errBoom
	if _, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia")); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("AddMembers = %v, want ErrRetryLater", err)
	}
	if _, err := rg.m.RemoveMember(t.Context(), remove("owen", "mia")); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("RemoveMember = %v, want ErrRetryLater", err)
	}
	if _, err := rg.m.LeaveRoom(t.Context(), leave("max")); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("LeaveRoom = %v, want ErrRetryLater", err)
	}
	if calls := rg.calls.list(); !slices.Equal(calls, []string{"arm", "arm", "arm"}) || len(rg.memberEvents()) != 0 {
		t.Fatalf("calls = %v, events %v; want nothing written", calls, rg.memberEvents())
	}
	if !rg.doc(t, "mia").Active() || !rg.doc(t, "max").Active() {
		t.Fatalf("a failed arm removed someone")
	}
	rg.timers.err = nil
	if got, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia")); err != nil || len(got) != 1 {
		t.Fatalf("retry after the arm works = %v, %v; want the request cancelled and nia added", users(got), err)
	}
}
