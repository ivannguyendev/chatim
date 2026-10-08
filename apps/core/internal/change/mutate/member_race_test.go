package mutate_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestALostCASIsRetryLaterAndWritesNothing(t *testing.T) {
	rg := newMemberRig(t, nil)
	rg.members.beforeApply = func() {
		cur := rg.doc(t, "mia")
		if ok, err := rg.rooms.ApplyMember(t.Context(), cur, cur.Next(cur.Role, domain.MemberActive, 5, "rival", "owen", created)); err != nil || !ok {
			t.Errorf("rival write = %v, %v", ok, err)
		}
	}
	if _, err := rg.m.RemoveMember(t.Context(), remove("owen", "mia")); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("RemoveMember after a rival write = %v, want ErrRetryLater", err)
	}
	if mia := rg.doc(t, "mia"); !mia.Active() || mia.Priority != 5 {
		t.Fatalf("mia = %+v, want only the rival's write", mia)
	}
	if calls := rg.calls.list(); !slices.Equal(calls, []string{"arm", "apply_member", "disarm"}) {
		t.Fatalf("calls = %v, want the timer disarmed after the lost write", calls)
	}
	if ev, c := rg.memberEvents(), rg.memberCount(t); len(ev) != 0 || len(rg.forgets.list()) != 0 || c.Count != 4 {
		t.Fatalf("events %v, forgets %v, count %+v; want nothing", ev, rg.forgets.list(), c)
	}
}

func TestAnAdminDemotedJustBeforeTheWriteStillRemoves(t *testing.T) {
	rg := newMemberRig(t, nil)
	rg.members.beforeApply = func() { rg.seedRole(t, "ada", domain.RoleMember) }
	res, err := rg.m.RemoveMember(t.Context(), remove("ada", "mia"))
	if err != nil || !res.Changed || rg.doc(t, "mia").Active() {
		t.Fatalf("RemoveMember by a just-demoted admin = %+v, %v; want the accepted gap to remove mia", res, err)
	}
	if ada := rg.doc(t, "ada"); ada.Role != domain.RoleMember {
		t.Fatalf("ada = %+v, want demoted", ada)
	}
}

func TestATargetPromotedMeanwhileMakesThePlainWriteFail(t *testing.T) {
	rg := newMemberRig(t, nil)
	rg.members.beforeApply = func() { rg.seedRole(t, "max", domain.RoleOwner) }
	if _, err := rg.m.RemoveMember(t.Context(), remove("owen", "max")); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("RemoveMember of a target promoted meanwhile = %v, want ErrRetryLater", err)
	}
	if got := rg.doc(t, "max"); !got.Active() || got.Role != domain.RoleOwner {
		t.Fatalf("max = %+v, want still an owner", got)
	}
	res, err := rg.m.RemoveMember(t.Context(), remove("owen", "max"))
	if err != nil || !res.Changed || res.Successor != "" || rg.doc(t, "max").Active() {
		t.Fatalf("retried RemoveMember = %+v, %v; want max removed by the owner transaction", res, err)
	}
	want := []string{"arm", "apply_member", "disarm", "change_owners"}
	if calls := rg.calls.list(); !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if c := rg.memberCount(t); c != (domain.MemberCount{Count: 3, Ver: 2}) {
		t.Fatalf("count = %+v, want 3 from the transaction", c)
	}
}

func TestTwoOwnersLeavingAtOnceOneGetsRetryLater(t *testing.T) {
	rg := newMemberRig(t, nil)
	rg.seedRole(t, "ada", domain.RoleOwner)
	rg.members.duringDecide = func() {
		if res, err := rg.m.LeaveRoom(t.Context(), leave("ada")); err != nil || res.Successor != "" {
			t.Errorf("ada leaves inside owen's transaction = %+v, %v", res, err)
		}
	}
	if _, err := rg.m.LeaveRoom(t.Context(), leave("owen")); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("owen leaves while ada left = %v, want ErrRetryLater", err)
	}
	if owen := rg.doc(t, "owen"); !owen.Active() || owen.Role != domain.RoleOwner {
		t.Fatalf("owen = %+v, want still the owner", owen)
	}
	res, err := rg.m.LeaveRoom(t.Context(), leave("owen"))
	if err != nil || res.Successor != "max" {
		t.Fatalf("owen leaves again = %+v, %v; want max as successor", res, err)
	}
	if ev := rg.memberEvents(); !slices.Equal(ev, []string{"removed ada", "count 3", "role max", "removed owen", "count 2"}) {
		t.Fatalf("events = %v", ev)
	}
}
