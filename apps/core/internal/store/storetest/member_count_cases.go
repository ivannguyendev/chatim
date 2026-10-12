package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func memberCountCases() []memberCase {
	return []memberCase{
		{"a new room counts its creators at member count ver 1", countNewRoom},
		{"add member count moves the count and bumps its ver", countAdd},
		{"add member count rejects zero and a missing room", countAddInvalid},
		{"count members counts active docs only", countActive},
		{"set member count writes only on the base ver", countSet},
	}
}

func countNewRoom(t *testing.T, s MemberRooms) {
	crew(t, s)
	r, err := s.Get(t.Context(), roomA)
	if err != nil || r.MemberCount != 2 || r.MemberCountVer != 1 {
		t.Fatalf("Get = %+v, %v; want member count 2 at ver 1", r, err)
	}
}

func countAdd(t *testing.T, s MemberRooms) {
	crew(t, s)
	for _, c := range []struct {
		delta int
		want  domain.MemberCount
	}{{2, domain.MemberCount{Count: 4, Ver: 2}}, {-1, domain.MemberCount{Count: 3, Ver: 3}}} {
		got, err := s.AddMemberCount(t.Context(), roomA, c.delta)
		if err != nil || got != c.want {
			t.Fatalf("AddMemberCount(%d) = %+v, %v; want %+v", c.delta, got, err, c.want)
		}
	}
	want := group(roomA, "Team", 3)
	want.MemberCountVer = 3
	assertRoom(t, s, want)
}

func countAddInvalid(t *testing.T, s MemberRooms) {
	crew(t, s)
	_, err := s.AddMemberCount(t.Context(), roomA, 0)
	assertErrorIs(t, "AddMemberCount(0)", err, apperr.ErrInvalidArgument)
	_, err = s.AddMemberCount(t.Context(), roomB, 1)
	assertErrorIs(t, "AddMemberCount(missing room)", err, domain.ErrRoomNotFound)
	assertRoom(t, s, group(roomA, "Team", 2))
}

func countActive(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	j := domain.Join{Room: roomA, Tenant: tenant, RequestID: "req-add", By: "alice", At: secs(1)}
	if _, err := s.AddMembers(t.Context(), j, []string{"carol", "dave"}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	removeMember(t, s, bob, secs(2))
	for room, want := range map[uint64]int{roomA: 3, roomB: 0} {
		if got, err := s.CountMembers(t.Context(), room); err != nil || got != want {
			t.Fatalf("CountMembers(%d) = %d, %v; want %d", room, got, err, want)
		}
	}
}

func countSet(t *testing.T, s MemberRooms) {
	crew(t, s)
	got, ok, err := s.SetMemberCount(t.Context(), roomA, 1, 5)
	if err != nil || !ok || got != (domain.MemberCount{Count: 5, Ver: 2}) {
		t.Fatalf("SetMemberCount(base 1) = %+v, %v, %v; want 5 at ver 2", got, ok, err)
	}
	if _, ok, err := s.SetMemberCount(t.Context(), roomA, 1, 9); ok || err != nil {
		t.Fatalf("SetMemberCount(stale base) = %v, %v; want false", ok, err)
	}
	if _, ok, err := s.SetMemberCount(t.Context(), roomB, 0, 1); ok || err != nil {
		t.Fatalf("SetMemberCount(missing room) = %v, %v; want false", ok, err)
	}
	_, _, err = s.SetMemberCount(t.Context(), roomA, 2, -1)
	assertErrorIs(t, "SetMemberCount(-1)", err, apperr.ErrInvalidArgument)
	want := group(roomA, "Team", 5)
	want.MemberCountVer = 2
	assertRoom(t, s, want)
}
