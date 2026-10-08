package mutate_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
)

func users(docs []domain.Member) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.User
	}
	return out
}

func TestAddMembersWritesForgetsAnnouncesAndCommitsTheRequest(t *testing.T) {
	rg := newMemberRig(t, nil)
	rg.post(t, 1)
	rg.post(t, 2)
	got, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia", "kai", "nia"))
	if err != nil || !slices.Equal(users(got), []string{"nia", "kai"}) {
		t.Fatalf("AddMembers = %v, %v; want nia and kai", users(got), err)
	}
	for _, d := range got {
		if !d.Active() || d.Role != domain.RoleMember || d.RequestID != "r1" || d.UpdatedBy != "owen" || d.ReadSeq != 2 || d.Ver != 1 || !d.JoinedAt.Equal(rg.at()) {
			t.Fatalf("added doc = %+v, want an active member of r1 at read seq 2", d)
		}
	}
	if forgot := rg.forgets.list(); !slices.Equal(forgot, []uint64{group}) {
		t.Fatalf("forgotten rooms = %v, want [%d]", forgot, group)
	}
	rooms, _ := rg.events.list()
	if ev := rg.memberEvents(); !slices.Equal(ev, []string{"added nia", "added kai", "count 6"}) || slices.ContainsFunc(rooms, func(r uint64) bool { return r != group }) {
		t.Fatalf("events = %v on rooms %v, want nia, kai then the count on the group", ev, rooms)
	}
	stored, err := rg.redis.Get(dedupe.RequestKey(group, "owen", "r1").String())
	if err != nil || !strings.HasPrefix(stored, "c:2:") {
		t.Fatalf("request key = %q, %v; want committed with two users", stored, err)
	}
	again, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia", "kai"))
	if err != nil || !slices.Equal(users(again), []string{"nia", "kai"}) {
		t.Fatalf("retried AddMembers = %v, %v; want the same members", users(again), err)
	}
	if ev := rg.memberEvents(); len(ev) != 3 || rg.timers.pending() != 0 || len(rg.calls.list()) != 4 {
		t.Fatalf("retry produced events %v, calls %v, pending timers %d", ev, rg.calls.list(), rg.timers.pending())
	}
}

func TestAddingActiveMembersChangesNothing(t *testing.T) {
	rg := newMemberRig(t, nil)
	got, err := rg.m.AddMembers(t.Context(), add("ada", "r1", "mia", "max"))
	if err != nil || len(got) != 0 {
		t.Fatalf("AddMembers of active members = %v, %v; want none added", users(got), err)
	}
	if ev, c := rg.memberEvents(), rg.memberCount(t); len(ev) != 0 || c != (domain.MemberCount{Count: 4, Ver: 1}) {
		t.Fatalf("events %v, count %+v; want nothing changed", ev, c)
	}
	if calls := rg.calls.list(); !slices.Equal(calls, []string{"arm", "add_members", "disarm"}) || len(rg.forgets.list()) != 0 {
		t.Fatalf("calls = %v, forgets = %v; want the timer disarmed and no forget", calls, rg.forgets.list())
	}
}

func TestReAddingRestoresAccessAsAMemberAndRaisesTheReadPosition(t *testing.T) {
	rg := newMemberRig(t, nil)
	rg.post(t, 1)
	if _, err := rg.m.RemoveMember(t.Context(), remove("owen", "ada")); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, err := rg.rooms.Member(t.Context(), group, "ada"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("removed ada still a member: %v", err)
	}
	gone := rg.doc(t, "ada")
	rg.post(t, 2)
	rg.post(t, 3)
	got, err := rg.m.AddMembers(t.Context(), add("owen", "r2", "ada"))
	if err != nil || len(got) != 1 {
		t.Fatalf("re-add = %v, %v", got, err)
	}
	back := got[0]
	if back.Role != domain.RoleMember || back.PreviousRole != domain.RoleAdmin || back.Ver != gone.Ver+1 || back.ReadSeq != 3 || back.ReadVer != gone.ReadVer+1 {
		t.Fatalf("re-added doc = %+v, want a plain member at read seq 3 after %+v", back, gone)
	}
	if m, err := rg.rooms.Member(t.Context(), group, "ada"); err != nil || m.Role != domain.RoleMember {
		t.Fatalf("Member(ada) = %+v, %v; want access back as a member", m, err)
	}
}

func TestARetryWithTheSameRequestNeverReAddsSomeoneRemovedSince(t *testing.T) {
	rg := newMemberRig(t, nil)
	if _, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia")); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	if _, err := rg.m.RemoveMember(t.Context(), remove("owen", "nia")); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	for _, restart := range []bool{false, true} {
		if restart {
			rg.restart(t)
		}
		got, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia"))
		if err != nil || len(got) != 0 || rg.doc(t, "nia").Active() {
			t.Fatalf("retry (restart %v) = %v, %v; want nobody re-added", restart, users(got), err)
		}
	}
}

func TestARequestStillRunningElsewhereIsRetryLater(t *testing.T) {
	rg := newMemberRig(t, nil)
	other := newRegistry(t, rg.rdb, "core-2")
	if v, err := other.Reserve(t.Context(), []dedupe.Key{dedupe.RequestKey(group, "owen", "r1")}); err != nil || v[0].Status != dedupe.Reserved {
		t.Fatalf("core-2 reserve = %v, %v", v, err)
	}
	if _, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia")); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("AddMembers while core-2 runs it = %v, want ErrRetryLater", err)
	}
	if calls := rg.calls.list(); len(calls) != 0 {
		t.Fatalf("calls = %v, want nothing armed or written", calls)
	}
}

func TestAFailedAddCancelsTheRequestAndEventFailuresAreIgnored(t *testing.T) {
	rg := newMemberRig(t, nil)
	rg.members.addErr = errBoom
	if _, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia")); !errors.Is(err, errBoom) {
		t.Fatalf("AddMembers = %v, want errBoom", err)
	}
	if rg.redis.Exists(dedupe.RequestKey(group, "owen", "r1").String()) {
		t.Fatalf("the request key outlived a failed add")
	}
	rg.members.addErr, rg.events.err = nil, errBoom
	if got, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "nia")); err != nil || len(got) != 1 {
		t.Fatalf("retry with refused events = %v, %v; want nia added", users(got), err)
	}
}

func TestABatchOfTheMaximumSizeIsOneWrite(t *testing.T) {
	rg := newMemberRig(t, nil)
	many := make([]string, mutate.DefaultMemberBatch+1)
	for i := range many {
		many[i] = "u" + strconv.Itoa(i)
	}
	if _, err := rg.m.AddMembers(t.Context(), add("owen", "r1", many...)); !errors.Is(err, domain.ErrTooManyMembers) {
		t.Fatalf("AddMembers of %d = %v, want ErrTooManyMembers", len(many), err)
	}
	got, err := rg.m.AddMembers(t.Context(), add("owen", "r2", append(many[:mutate.DefaultMemberBatch], "u0")...))
	if err != nil || len(got) != mutate.DefaultMemberBatch {
		t.Fatalf("AddMembers of the cap = %d, %v", len(got), err)
	}
	if calls := rg.calls.list(); !slices.Equal(calls, []string{"arm", "add_members", "add_member_count", "disarm"}) {
		t.Fatalf("calls = %v, want one member write", calls)
	}
}
