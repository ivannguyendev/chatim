package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func memberListCases() []memberCase {
	return []memberCase{
		{"list members reads owners, then admins, then members, each by priority, join time and user", listMembersOrder},
		{"list members pages through a cursor across the role boundaries", listMembersPages},
		{"invalid member lists are rejected", listMembersInvalid},
	}
}

func rankedMember(user string, role domain.Role, priority int32, joined int) domain.Member {
	m := member(roomA, user, role)
	m.Priority, m.JoinedAt = priority, secs(joined)
	return m
}

func seedRankedMembers(t *testing.T, s MemberRooms) []domain.Member {
	t.Helper()
	members := []domain.Member{
		rankedMember("zed", domain.RoleMember, 0, 1), rankedMember("carl", domain.RoleAdmin, 5, 2),
		rankedMember("alice", domain.RoleOwner, 0, 0), rankedMember("bob", domain.RoleMember, 2, 4),
		rankedMember("amy", domain.RoleMember, 0, 1), rankedMember("dave", domain.RoleAdmin, 9, 3),
		rankedMember("bea", domain.RoleAdmin, 5, 1), rankedMember("gone", domain.RoleAdmin, 9, 0),
	}
	mustCreate(t, s, group(roomA, "Team", len(members)), members)
	other, others := teamOf(roomB)
	mustCreate(t, s, other, others)
	removeMember(t, s, docOf(t, s, roomA, "gone"), secs(9))
	return membersOf(t, s, roomA, "alice", "dave", "bea", "carl", "bob", "amy", "zed")
}

func listMembersOrder(t *testing.T, s MemberRooms) {
	want := seedRankedMembers(t, s)
	assertListMembers(t, s, store.MemberCursor{}, 10, want...)
	assertListMembers(t, s, store.MemberCursor{}, 1, want[0])
}

func listMembersPages(t *testing.T, s MemberRooms) {
	want := seedRankedMembers(t, s)
	after := store.MemberCursor{}
	for start := 0; start < len(want); start += 3 {
		page := want[start:min(start+3, len(want))]
		assertListMembers(t, s, after, 3, page...)
		after = store.MemberCursorOf(page[len(page)-1])
	}
	assertListMembers(t, s, after, 3)
	assertListMembers(t, s, store.MemberCursorOf(want[3]), 1, want[4])
	assertListMembers(t, s, store.MemberCursorOf(want[0]), 1, want[1])
	between := store.MemberCursor{Role: domain.RoleAdmin, Priority: 5, JoinedAt: secs(1).Add(time.Millisecond), User: "a"}
	assertListMembers(t, s, between, 2, want[3], want[4])
}

func listMembersInvalid(t *testing.T, s MemberRooms) {
	cursor := store.MemberCursor{Role: domain.RoleAdmin, JoinedAt: baseTime, User: "bob"}
	for name, c := range map[string]struct {
		room  uint64
		after store.MemberCursor
		limit int
	}{
		"zero room":           {0, store.MemberCursor{}, 10},
		"zero limit":          {roomA, store.MemberCursor{}, 0},
		"limit too big":       {roomA, store.MemberCursor{}, store.MaxMemberPage + 1},
		"unknown cursor role": {roomA, store.MemberCursor{Role: "guest", JoinedAt: baseTime, User: "bob"}, 10},
		"cursor without time": {roomA, store.MemberCursor{Role: domain.RoleAdmin, User: "bob"}, 10},
		"bad cursor user":     {roomA, store.MemberCursor{Role: cursor.Role, JoinedAt: cursor.JoinedAt, User: "a.b"}, 10},
	} {
		_, err := s.ListMembers(t.Context(), c.room, c.after, c.limit)
		assertErrorIs(t, "ListMembers("+name+")", err, apperr.ErrInvalidArgument)
	}
}

func assertListMembers(t *testing.T, s MemberRooms, after store.MemberCursor, limit int, want ...domain.Member) {
	t.Helper()
	got, err := s.ListMembers(t.Context(), roomA, after, limit)
	if err != nil {
		t.Fatalf("ListMembers(after %+v, limit %d): %v", after, limit, err)
	}
	assertMembers(t, "ListMembers", got, want)
}
