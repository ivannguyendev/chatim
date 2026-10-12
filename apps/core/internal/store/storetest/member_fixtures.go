package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func secs(n int) time.Time { return baseTime.Add(time.Duration(n) * time.Second) }

func sameMember(a, b domain.Member) bool {
	times := [][2]time.Time{{a.JoinedAt, b.JoinedAt}, {a.ClearedAt, b.ClearedAt}, {a.UpdatedAt, b.UpdatedAt}, {a.LastChangeAt, b.LastChangeAt}}
	for _, p := range times {
		if !p[0].Equal(p[1]) {
			return false
		}
	}
	for _, m := range []*domain.Member{&a, &b} {
		m.JoinedAt, m.ClearedAt, m.UpdatedAt, m.LastChangeAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	}
	return a == b
}

func assertMembers(t *testing.T, op string, got, want []domain.Member) {
	t.Helper()
	if !slices.EqualFunc(got, want, sameMember) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func membersOf(t *testing.T, s MemberRooms, room uint64, users ...string) []domain.Member {
	t.Helper()
	got, err := s.MembersOf(t.Context(), room, users)
	if err != nil {
		t.Fatalf("MembersOf(%d, %v): %v", room, users, err)
	}
	return got
}

func docOf(t *testing.T, s MemberRooms, room uint64, user string) domain.Member {
	t.Helper()
	got := membersOf(t, s, room, user)
	if len(got) != 1 {
		t.Fatalf("MembersOf(%d, %q) = %+v, want one doc", room, user, got)
	}
	return got[0]
}

func mustApplyMember(t *testing.T, s MemberRooms, cur, next domain.Member) {
	t.Helper()
	ok, err := s.ApplyMember(t.Context(), cur, next)
	if err != nil || !ok {
		t.Fatalf("ApplyMember(%q ver %d -> %d) = %v, %v; want written", cur.User, cur.Ver, next.Ver, ok, err)
	}
}

func changeMember(t *testing.T, s MemberRooms, cur domain.Member, role domain.Role, state domain.MemberState, priority int32, at time.Time) domain.Member {
	t.Helper()
	next := cur.Next(role, state, priority, "req-change", "alice", at)
	mustApplyMember(t, s, cur, next)
	return next
}

func removeMember(t *testing.T, s MemberRooms, cur domain.Member, at time.Time) domain.Member {
	t.Helper()
	return changeMember(t, s, cur, cur.Role, domain.MemberRemoved, cur.Priority, at)
}
