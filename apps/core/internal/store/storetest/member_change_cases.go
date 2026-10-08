package storetest

import (
	"fmt"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func memberChangeCases() []memberCase {
	return []memberCase{
		{"apply member writes role, state, priority and their previous values", applyMemberWrites},
		{"apply member with a stale ver or without a doc writes nothing", applyMemberStale},
		{"apply member changes only the membership fields", applyMemberOnlyMembership},
		{"apply member rejects an invalid step", applyMemberInvalid},
		{"members between scans last change times in order", membersBetween},
	}
}

func applyMemberWrites(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	admin := changeMember(t, s, bob, domain.RoleAdmin, domain.MemberActive, 7, secs(5))
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob"), []domain.Member{admin})
	if admin.Priority != 7 || admin.PreviousPriority != 0 || admin.PreviousRole != domain.RoleMember || admin.Ver != 2 || !admin.LastChangeAt.Equal(secs(5)) {
		t.Fatalf("admin = %+v, want priority 7 from 0, role admin from member, ver 2, changed at %v", admin, secs(5))
	}
	lower := changeMember(t, s, admin, domain.RoleAdmin, domain.MemberActive, -3, secs(6))
	gone := removeMember(t, s, lower, secs(7))
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob"), []domain.Member{gone})
	if gone.PreviousPriority != -3 || gone.PreviousState != domain.MemberActive || gone.State != domain.MemberRemoved || gone.Ver != 4 {
		t.Fatalf("gone = %+v, want removed at ver 4 from active priority -3", gone)
	}
}

func applyMemberStale(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	first := changeMember(t, s, bob, domain.RoleAdmin, domain.MemberActive, 0, secs(5))
	again := bob.Next(domain.RoleOwner, domain.MemberActive, 0, "req-late", "alice", secs(6))
	if ok, err := s.ApplyMember(t.Context(), bob, again); ok || err != nil {
		t.Fatalf("ApplyMember(stale ver) = %v, %v; want false", ok, err)
	}
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob"), []domain.Member{first})
	ghost := bob
	ghost.User = "ghost"
	if ok, err := s.ApplyMember(t.Context(), ghost, ghost.Next(domain.RoleAdmin, domain.MemberActive, 0, "req-g", "alice", secs(6))); ok || err != nil {
		t.Fatalf("ApplyMember(no doc) = %v, %v; want false", ok, err)
	}
	assertMembers(t, "MembersOf(ghost)", membersOf(t, s, roomA, "ghost"), nil)
}

func applyMemberOnlyMembership(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	want := bob.Next(domain.RoleAdmin, domain.MemberActive, 2, "req-up", "alice", secs(5))
	next := want
	next.JoinedAt, next.ClearedAt, next.ReadSeq, next.ReadVer, next.Tenant = secs(50), secs(60), 99, 5, "other"
	mustApplyMember(t, s, bob, next)
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob"), []domain.Member{want})
}

func applyMemberInvalid(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	skip := bob.Next(domain.RoleAdmin, domain.MemberActive, 0, "req-up", "alice", secs(5))
	skip.Ver = bob.Ver + 2
	badRole := bob.Next("guest", domain.MemberActive, 0, "req-up", "alice", secs(5))
	for name, next := range map[string]domain.Member{"skipped ver": skip, "bad role": badRole} {
		_, err := s.ApplyMember(t.Context(), bob, next)
		assertErrorIs(t, "ApplyMember("+name+")", err, apperr.ErrInvalidArgument)
	}
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob"), []domain.Member{bob})
}

func membersBetween(t *testing.T, s MemberRooms) {
	users := []string{"alice", "bob", "zed", "amy", "al", "carl"}
	members := make([]domain.Member, len(users))
	for i, u := range users {
		members[i] = member(roomA, u, domain.RoleMember)
	}
	mustCreate(t, s, group(roomA, "Team", len(users)), members)
	mustCreate(t, s, group(roomB, "Other", 1), []domain.Member{member(roomB, "al", domain.RoleOwner)})
	at := map[string]int{"bob": 2, "zed": 3, "amy": 3, "al": 3, "alice": 5}
	docs := map[string]domain.Member{"carl": created(members[5])}
	for i, u := range users[:5] {
		docs[u] = changeMember(t, s, created(members[i]), domain.RoleMember, domain.MemberActive, 1, secs(at[u]))
	}
	changeMember(t, s, created(member(roomB, "al", domain.RoleOwner)), domain.RoleOwner, domain.MemberActive, 1, secs(3))
	pick := func(names ...string) []domain.Member {
		out := []domain.Member{}
		for _, n := range names {
			out = append(out, docs[n])
		}
		return out
	}
	cases := []struct {
		from, to time.Time
		limit    int
		want     []domain.Member
	}{
		{secs(2), secs(3), store.MaxMemberScan, pick("bob", "al", "amy", "zed")},
		{secs(2), secs(3), 2, pick("bob", "al")},
		{baseTime, secs(2), 10, pick("carl", "bob")},
		{secs(6), secs(9), 10, nil},
	}
	for _, c := range cases {
		got, err := s.MembersBetween(t.Context(), roomA, c.from, c.to, c.limit)
		if err != nil {
			t.Fatalf("MembersBetween(%v, %v, %d): %v", c.from, c.to, c.limit, err)
		}
		assertMembers(t, fmt.Sprintf("MembersBetween(%v..%v, limit %d)", c.from, c.to, c.limit), got, c.want)
	}
	for _, limit := range []int{0, store.MaxMemberScan + 1} {
		_, err := s.MembersBetween(t.Context(), roomA, baseTime, secs(9), limit)
		assertErrorIs(t, fmt.Sprintf("MembersBetween(limit %d)", limit), err, apperr.ErrInvalidArgument)
	}
}
