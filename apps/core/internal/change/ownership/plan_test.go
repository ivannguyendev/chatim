package ownership_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var at = secs(100)

func allowAll(domain.Member, domain.Member) error { return nil }

func req(a ownership.Action, caller, target string, role domain.Role) ownership.Request {
	return ownership.Request{Action: a, Caller: caller, Target: target, Role: role, RequestID: "req-1", At: at, Allow: allowAll}
}

func left(m domain.Member) domain.Member {
	return m.Next(m.Role, domain.MemberRemoved, m.Priority, "req-0", m.User, secs(50))
}

func planOf(t *testing.T, r ownership.Request, v store.OwnerView) []store.MemberWrite {
	t.Helper()
	writes, err := ownership.Plan(r, v)
	if err != nil {
		t.Fatalf("Plan(%+v) error = %v", r, err)
	}
	return writes
}

func assertWrites(t *testing.T, got []store.MemberWrite, want ...store.MemberWrite) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Plan wrote %d docs %+v; want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("write %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

func to(cur domain.Member, role domain.Role, state domain.MemberState, by string) store.MemberWrite {
	return store.MemberWrite{Cur: cur, Next: cur.Next(role, state, cur.Priority, "req-1", by, at)}
}

func TestTheLastOwnerLeavingPromotesTheSuccessorFirst(t *testing.T) {
	alice, bob, carol := seat("alice", domain.RoleOwner, 0, 0), seat("bob", domain.RoleAdmin, 2, 3), seat("carol", domain.RoleMember, 1, 9)
	v := store.OwnerView{Docs: []domain.Member{alice}, Owners: []domain.Member{alice}, Candidates: []domain.Member{bob, carol}}
	writes := planOf(t, req(ownership.Leave, "alice", "", ""), v)
	assertWrites(t, writes, to(bob, domain.RoleOwner, domain.MemberActive, "alice"), to(alice, domain.RoleOwner, domain.MemberRemoved, "alice"))
	up, out := writes[0].Next, writes[1].Next
	if up.Role != domain.RoleOwner || up.PreviousRole != domain.RoleAdmin || up.Priority != 3 || up.Ver != 2 || up.UpdatedBy != "alice" || up.UpdatedAt != at {
		t.Fatalf("successor = %+v; want owner at ver 2 keeping priority 3", up)
	}
	if out.Active() || out.Role != domain.RoleOwner || out.RequestID != "req-1" || out.Ver != 2 || out.LastChangeAt != at {
		t.Fatalf("leaver = %+v; want a removed owner at ver 2", out)
	}
}

func TestAnOwnerLeavingBesideAnotherOwnerNamesNoSuccessor(t *testing.T) {
	alice, zoe, bob := seat("alice", domain.RoleOwner, 0, 0), seat("zoe", domain.RoleOwner, 1, 0), seat("bob", domain.RoleAdmin, 2, 0)
	v := store.OwnerView{Docs: []domain.Member{alice}, Owners: []domain.Member{alice, zoe}, Candidates: []domain.Member{bob}}
	assertWrites(t, planOf(t, req(ownership.Leave, "alice", "alice", ""), v), to(alice, domain.RoleOwner, domain.MemberRemoved, "alice"))
	v = store.OwnerView{Docs: []domain.Member{zoe, alice}, Owners: []domain.Member{alice, zoe}, Candidates: []domain.Member{bob}}
	assertWrites(t, planOf(t, req(ownership.Remove, "zoe", "alice", ""), v), to(alice, domain.RoleOwner, domain.MemberRemoved, "zoe"))
}

func TestTheOnlyMemberLeavingEmptiesTheGroup(t *testing.T) {
	alice := seat("alice", domain.RoleOwner, 0, 0)
	v := store.OwnerView{Docs: []domain.Member{alice}, Owners: []domain.Member{alice}}
	assertWrites(t, planOf(t, req(ownership.Leave, "alice", "", ""), v), to(alice, domain.RoleOwner, domain.MemberRemoved, "alice"))
}

func TestTheLastOwnerCannotStepDown(t *testing.T) {
	alice, bob := seat("alice", domain.RoleOwner, 0, 0), seat("bob", domain.RoleAdmin, 1, 0)
	v := store.OwnerView{Docs: []domain.Member{alice}, Owners: []domain.Member{alice}, Candidates: []domain.Member{bob}}
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember} {
		if writes, err := ownership.Plan(req(ownership.ChangeRole, "alice", "alice", role), v); !errors.Is(err, domain.ErrLastOwner) || writes != nil {
			t.Fatalf("Plan(step down to %s) = %+v, %v; want ErrLastOwner", role, writes, err)
		}
	}
	zoe := seat("zoe", domain.RoleOwner, 1, 0)
	v = store.OwnerView{Docs: []domain.Member{zoe, alice}, Owners: []domain.Member{alice, zoe}}
	assertWrites(t, planOf(t, req(ownership.ChangeRole, "zoe", "alice", domain.RoleAdmin), v), to(alice, domain.RoleAdmin, domain.MemberActive, "zoe"))
}

func TestPromotingToOwnerWritesOnlyTheTarget(t *testing.T) {
	alice, bob := seat("alice", domain.RoleOwner, 0, 0), seat("bob", domain.RoleAdmin, 1, 4)
	v := store.OwnerView{Docs: []domain.Member{alice, bob}, Owners: []domain.Member{alice}, Candidates: []domain.Member{bob}}
	writes := planOf(t, req(ownership.ChangeRole, "alice", "bob", domain.RoleOwner), v)
	assertWrites(t, writes, to(bob, domain.RoleOwner, domain.MemberActive, "alice"))
	if writes[0].Next.Priority != 4 {
		t.Fatalf("promoted priority = %d; want 4 kept", writes[0].Next.Priority)
	}
}

func TestPlanIsDesiredState(t *testing.T) {
	alice, gone := seat("alice", domain.RoleOwner, 0, 0), left(seat("bob", domain.RoleOwner, 1, 0))
	zoe := seat("zoe", domain.RoleOwner, 2, 0)
	cases := map[string]struct {
		r ownership.Request
		v store.OwnerView
	}{
		"leave as a tombstone":   {req(ownership.Leave, "bob", "", ""), store.OwnerView{Docs: []domain.Member{gone}, Owners: []domain.Member{alice}}},
		"remove a tombstone":     {req(ownership.Remove, "alice", "bob", ""), store.OwnerView{Docs: []domain.Member{alice, gone}, Owners: []domain.Member{alice}}},
		"change to the same one": {req(ownership.ChangeRole, "alice", "zoe", domain.RoleOwner), store.OwnerView{Docs: []domain.Member{alice, zoe}, Owners: []domain.Member{alice, zoe}}},
	}
	for name, c := range cases {
		if writes, err := ownership.Plan(c.r, c.v); err != nil || len(writes) != 0 {
			t.Errorf("%s: Plan = %+v, %v; want an empty plan", name, writes, err)
		}
	}
}

func TestPlanRejectsAnUnknownAction(t *testing.T) {
	alice := seat("alice", domain.RoleOwner, 0, 0)
	v := store.OwnerView{Docs: []domain.Member{alice}, Owners: []domain.Member{alice}}
	if _, err := ownership.Plan(req("promote", "alice", "alice", ""), v); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("Plan(unknown action) error = %v; want invalid argument", err)
	}
}

type allowCall struct{ caller, target domain.Member }

func recording(calls *[]allowCall, err error) func(domain.Member, domain.Member) error {
	return func(c, t domain.Member) error {
		*calls = append(*calls, allowCall{c, t})
		return err
	}
}

func TestPlanChecksTheCallerAndAsksAllowWithTheFreshDocs(t *testing.T) {
	alice, bob := seat("alice", domain.RoleOwner, 0, 0), seat("bob", domain.RoleMember, 1, 0)
	exAdmin := left(seat("erin", domain.RoleAdmin, 2, 0))
	standIn := func(u string) domain.Member { return domain.Member{User: u, Role: domain.RoleMember} }
	denied := errors.New("denied")
	cases := []struct {
		name    string
		r       ownership.Request
		docs    []domain.Member
		deny    error
		want    error
		allowed []allowCall
	}{
		{"tombstoned caller", req(ownership.Remove, "erin", "bob", ""), []domain.Member{exAdmin, bob}, nil, domain.ErrNotMember, nil},
		{"missing caller", req(ownership.Remove, "nobody", "bob", ""), []domain.Member{bob}, nil, domain.ErrNotMember, nil},
		{"active target", req(ownership.Remove, "alice", "bob", ""), []domain.Member{alice, bob}, denied, denied, []allowCall{{alice, bob}}},
		{"missing target to remove", req(ownership.Remove, "alice", "ghost", ""), []domain.Member{alice}, nil, domain.ErrMemberNotFound, []allowCall{{alice, standIn("ghost")}}},
		{"missing target denied first", req(ownership.ChangeRole, "alice", "ghost", domain.RoleAdmin), []domain.Member{alice}, denied, denied, []allowCall{{alice, standIn("ghost")}}},
		{"tombstoned target to change", req(ownership.ChangeRole, "alice", "erin", domain.RoleOwner), []domain.Member{alice, exAdmin}, nil, domain.ErrMemberNotFound, []allowCall{{alice, standIn("erin")}}},
		{"tombstoned former admin to remove", req(ownership.Remove, "alice", "erin", ""), []domain.Member{alice, exAdmin}, nil, nil, []allowCall{{alice, standIn("erin")}}},
	}
	for _, c := range cases {
		var calls []allowCall
		c.r.Allow = recording(&calls, c.deny)
		v := store.OwnerView{Docs: c.docs, Owners: []domain.Member{alice}}
		writes, err := ownership.Plan(c.r, v)
		if !errors.Is(err, c.want) || len(writes) != 0 {
			t.Errorf("%s: Plan = %+v, %v; want no writes and %v", c.name, writes, err, c.want)
		}
		if len(calls) != len(c.allowed) || (len(calls) == 1 && calls[0] != c.allowed[0]) {
			t.Errorf("%s: Allow calls = %+v; want %+v", c.name, calls, c.allowed)
		}
	}
}

func TestAffectsOnlyOwnerTargetsAndPromotions(t *testing.T) {
	owner, admin, plain := seat("alice", domain.RoleOwner, 0, 0), seat("bob", domain.RoleAdmin, 0, 0), seat("carol", domain.RoleMember, 0, 0)
	cases := []struct {
		target domain.Member
		action ownership.Action
		role   domain.Role
		want   bool
	}{
		{owner, ownership.Leave, "", true},
		{owner, ownership.Remove, "", true},
		{owner, ownership.ChangeRole, domain.RoleAdmin, true},
		{left(owner), ownership.Remove, "", false},
		{admin, ownership.Remove, "", false},
		{admin, ownership.Leave, "", false},
		{admin, ownership.ChangeRole, domain.RoleMember, false},
		{admin, ownership.ChangeRole, domain.RoleOwner, true},
		{plain, ownership.ChangeRole, domain.RoleOwner, true},
	}
	for _, c := range cases {
		if got := ownership.Affects(c.target, c.action, c.role); got != c.want {
			t.Errorf("Affects(%s %s active=%v, %s, %q) = %v; want %v", c.target.User, c.target.Role, c.target.Active(), c.action, c.role, got, c.want)
		}
	}
}
