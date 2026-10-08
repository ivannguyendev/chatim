package storetest

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func ownerChangeCases() []memberCase {
	return []memberCase{
		{"change owners shows docs, two owners and the top admin and member candidates", ownerView},
		{"change owners writes the plan in order and bumps the owners ver and the member count", ownerWritesPlan},
		{"change owners of a role only keeps the member count", ownerRoleKeepsCount},
		{"change owners writes nothing when decide fails", ownerDecideFails},
		{"change owners writes nothing when a later write is stale", ownerStaleWrite},
		{"change owners with an empty plan writes nothing", ownerEmptyPlan},
		{"change owners loses to a change committed inside decide", ownerNestedCommit},
		{"change owners of a missing room is not found", ownerMissingRoom},
	}
}

func seat(user string, role domain.Role, joinedSec int, priority int32) domain.Member {
	m := member(roomA, user, role)
	m.JoinedAt, m.Priority = secs(joinedSec), priority
	return m
}

func viewOf(t *testing.T, s MemberRooms, room uint64, users ...string) store.OwnerView {
	t.Helper()
	var seen store.OwnerView
	res, err := s.ChangeOwners(t.Context(), room, users, func(v store.OwnerView) ([]store.MemberWrite, error) {
		seen = v
		return nil, nil
	})
	if err != nil || len(res.Written) != 0 || res.CountChanged {
		t.Fatalf("ChangeOwners(empty plan) = %+v, %v; want nothing written", res, err)
	}
	return seen
}

func plan(writes ...store.MemberWrite) store.OwnerDecision {
	return func(store.OwnerView) ([]store.MemberWrite, error) { return writes, nil }
}

func step(cur domain.Member, role domain.Role, state domain.MemberState) store.MemberWrite {
	return store.MemberWrite{Cur: cur, Next: cur.Next(role, state, cur.Priority, "req-owner", "alice", secs(30))}
}

func ownerView(t *testing.T, s MemberRooms) {
	seats := []domain.Member{
		seat("alice", domain.RoleOwner, 1, 0), seat("zoe", domain.RoleOwner, 0, 0), seat("bob", domain.RoleOwner, 0, 0),
		seat("adam", domain.RoleOwner, -5, 0), seat("ivan", domain.RoleAdmin, 0, 9),
		seat("carol", domain.RoleAdmin, 1, 0), seat("dave", domain.RoleAdmin, 3, 5), seat("erin", domain.RoleAdmin, 2, 5),
		seat("frank", domain.RoleMember, 1, 0), seat("hank", domain.RoleMember, 4, 2), seat("gina", domain.RoleMember, 4, 2),
	}
	mustCreate(t, s, group(roomA, "Team", len(seats)), seats)
	docs := map[string]domain.Member{}
	for _, m := range seats {
		docs[m.User] = created(m)
	}
	docs["adam"] = removeMember(t, s, docs["adam"], secs(9))
	docs["ivan"] = removeMember(t, s, docs["ivan"], secs(9))
	v := viewOf(t, s, roomA, "zoe", "nobody", "ivan")
	if v.OwnersVer != 0 {
		t.Fatalf("OwnersVer = %d, want 0 on a new room", v.OwnersVer)
	}
	assertMembers(t, "Docs", v.Docs, []domain.Member{docs["zoe"], docs["ivan"]})
	assertMembers(t, "Owners", v.Owners, []domain.Member{docs["bob"], docs["zoe"]})
	assertMembers(t, "Candidates", v.Candidates, []domain.Member{docs["erin"], docs["gina"]})
}

func ownerWritesPlan(t *testing.T, s MemberRooms) {
	alice, bob := crew(t, s)
	up, out := step(bob, domain.RoleOwner, domain.MemberActive), step(alice, domain.RoleOwner, domain.MemberRemoved)
	res, err := s.ChangeOwners(t.Context(), roomA, []string{"alice", "bob"}, plan(up, out))
	if err != nil || !res.CountChanged || res.Count != (domain.MemberCount{Count: 1, Ver: 2}) {
		t.Fatalf("ChangeOwners = %+v, %v; want the count at 1 ver 2", res, err)
	}
	assertMembers(t, "Written", res.Written, []domain.Member{up.Next, out.Next})
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob", "alice"), []domain.Member{up.Next, out.Next})
	if v := viewOf(t, s, roomA, "bob"); v.OwnersVer != 1 {
		t.Fatalf("OwnersVer = %d, want 1", v.OwnersVer)
	}
	want := group(roomA, "Team", 1)
	want.MemberCountVer = 2
	assertRoom(t, s, want)
}

func ownerRoleKeepsCount(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	res, err := s.ChangeOwners(t.Context(), roomA, []string{"bob"}, plan(step(bob, domain.RoleOwner, domain.MemberActive)))
	if err != nil || res.CountChanged || len(res.Written) != 1 {
		t.Fatalf("ChangeOwners(role) = %+v, %v; want one write and no count change", res, err)
	}
	assertRoom(t, s, group(roomA, "Team", 2))
}

func ownerDecideFails(t *testing.T, s MemberRooms) {
	alice, bob := crew(t, s)
	boom := errors.New("boom")
	_, err := s.ChangeOwners(t.Context(), roomA, []string{"bob"}, func(store.OwnerView) ([]store.MemberWrite, error) { return nil, boom })
	assertErrorIs(t, "ChangeOwners", err, boom)
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "alice", "bob"), []domain.Member{alice, bob})
	if v := viewOf(t, s, roomA, "bob"); v.OwnersVer != 0 {
		t.Fatalf("OwnersVer = %d, want 0", v.OwnersVer)
	}
}

func ownerStaleWrite(t *testing.T, s MemberRooms) {
	alice, bob := crew(t, s)
	moved := changeMember(t, s, alice, domain.RoleOwner, domain.MemberActive, 4, secs(5))
	_, err := s.ChangeOwners(t.Context(), roomA, []string{"alice", "bob"}, plan(step(bob, domain.RoleOwner, domain.MemberActive), step(alice, domain.RoleOwner, domain.MemberRemoved)))
	assertErrorIs(t, "ChangeOwners", err, domain.ErrRetryLater)
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "alice", "bob"), []domain.Member{moved, bob})
	if v := viewOf(t, s, roomA, "bob"); v.OwnersVer != 0 {
		t.Fatalf("OwnersVer = %d, want 0", v.OwnersVer)
	}
	assertRoom(t, s, group(roomA, "Team", 2))
}

func ownerEmptyPlan(t *testing.T, s MemberRooms) {
	alice, bob := crew(t, s)
	viewOf(t, s, roomA, "alice")
	if v := viewOf(t, s, roomA, "bob"); v.OwnersVer != 0 {
		t.Fatalf("OwnersVer = %d, want 0 after an empty plan", v.OwnersVer)
	}
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "alice", "bob"), []domain.Member{alice, bob})
}

func ownerNestedCommit(t *testing.T, s MemberRooms) {
	alice, bob := crew(t, s)
	inner := step(bob, domain.RoleAdmin, domain.MemberActive)
	_, err := s.ChangeOwners(t.Context(), roomA, []string{"alice"}, func(store.OwnerView) ([]store.MemberWrite, error) {
		if _, err := s.ChangeOwners(t.Context(), roomA, []string{"bob"}, plan(inner)); err != nil {
			t.Errorf("inner ChangeOwners: %v", err)
		}
		return []store.MemberWrite{step(alice, domain.RoleOwner, domain.MemberRemoved)}, nil
	})
	assertErrorIs(t, "outer ChangeOwners", err, domain.ErrRetryLater)
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "alice", "bob"), []domain.Member{alice, inner.Next})
	if v := viewOf(t, s, roomA, "bob"); v.OwnersVer != 1 {
		t.Fatalf("OwnersVer = %d, want 1 from the inner change only", v.OwnersVer)
	}
	assertRoom(t, s, group(roomA, "Team", 2))
}

func ownerMissingRoom(t *testing.T, s MemberRooms) {
	crew(t, s)
	called := false
	_, err := s.ChangeOwners(t.Context(), roomB, []string{"alice"}, func(store.OwnerView) ([]store.MemberWrite, error) {
		called = true
		return nil, nil
	})
	assertErrorIs(t, "ChangeOwners", err, domain.ErrRoomNotFound)
	if called {
		t.Fatal("decide ran for a missing room")
	}
}
