package storetest

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type MemberRooms interface {
	store.Rooms
	store.HistoryClearer
	store.MemberWriter
	store.MemberReader
	store.OwnerChanges
	store.MemberCounts
	store.ReadPositions
}

type memberCase struct {
	name string
	run  func(t *testing.T, s MemberRooms)
}

func RunMembers(t *testing.T, open func(t *testing.T) MemberRooms) {
	t.Helper()
	for _, c := range slices.Concat(memberCases(), memberChangeCases(), ownerChangeCases(), memberCountCases(), readPositionCases()) {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func memberCases() []memberCase {
	return []memberCase{
		{"create writes an active ver 1 doc for each creator", memberCreateDocs},
		{"member rejects a tombstone that members of still returns", memberTombstone},
		{"members of keeps the asked order and skips users without a doc", membersOfOrder},
		{"add members adds, re-adds a tombstone and keeps active docs", addMembersMixed},
		{"add members of the same users again changes nothing", addMembersAgain},
		{"add members rejects an invalid join and writes nothing", addMembersInvalid},
	}
}

func crew(t *testing.T, s MemberRooms) (alice, bob domain.Member) {
	t.Helper()
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	return created(members[0]), created(members[1])
}

func memberCreateDocs(t *testing.T, s MemberRooms) {
	alice, bob := crew(t, s)
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "alice", "bob"), []domain.Member{alice, bob})
	for _, m := range []domain.Member{alice, bob} {
		if !m.Active() || m.Ver != 1 || m.RequestID != strconv.FormatUint(roomA, 10)+"-created" || m.UpdatedBy != "alice" {
			t.Fatalf("created member = %+v, want active at ver 1 by request %d-created", m, roomA)
		}
	}
}

func memberTombstone(t *testing.T, s MemberRooms) {
	alice, bob := crew(t, s)
	gone := removeMember(t, s, bob, secs(5))
	assertNotMember(t, s, roomA, "bob")
	assertMember(t, s, alice)
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob"), []domain.Member{gone})
}

func membersOfOrder(t *testing.T, s MemberRooms) {
	alice, bob := crew(t, s)
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob", "nobody", "alice"), []domain.Member{bob, alice})
	assertMembers(t, "MembersOf(other room)", membersOf(t, s, roomB, "alice"), nil)
	many := make([]string, domain.MaxMemberBatch+1)
	for i := range many {
		many[i] = "u" + strconv.Itoa(i)
	}
	many[0] = "alice"
	assertMembers(t, "MembersOf(max)", membersOf(t, s, roomA, many...), []domain.Member{alice})
	for name, users := range map[string][]string{"none": nil, "too many": append(many, "extra")} {
		_, err := s.MembersOf(t.Context(), roomA, users)
		assertErrorIs(t, "MembersOf("+name+")", err, apperr.ErrInvalidArgument)
	}
}

func addMembersMixed(t *testing.T, s MemberRooms) {
	alice, bob := crew(t, s)
	if _, _, err := s.MarkRead(t.Context(), roomA, "bob", 50); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if _, _, err := s.ClearHistory(t.Context(), roomA, "bob", secs(2)); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	read := docOf(t, s, roomA, "bob")
	admin := read.Next(domain.RoleAdmin, domain.MemberActive, 3, "req-up", "alice", secs(3))
	mustApplyMember(t, s, read, admin)
	gone := removeMember(t, s, admin, secs(4))
	j := domain.Join{Room: roomA, Tenant: tenant, RequestID: "req-add", By: "alice", At: secs(10), ReadSeq: 30}
	res, err := s.AddMembers(t.Context(), j, []string{"carol", "bob", "alice"})
	if err != nil || res.Changed != 2 {
		t.Fatalf("AddMembers = %+v, %v; want 2 changed", res, err)
	}
	want := []domain.Member{j.Apply(domain.Member{}, "carol"), j.Apply(gone, "bob"), alice}
	assertMembers(t, "AddMembers", res.Members, want)
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "carol", "bob", "alice"), want)
	back := want[1]
	if back.Role != domain.RoleMember || back.ReadSeq != 50 || !back.ClearedAt.Equal(secs(2)) || back.Ver != bob.Ver+3 {
		t.Fatalf("re-added member = %+v, want role member, read seq 50, cleared at %v, ver %d", back, secs(2), bob.Ver+3)
	}
	assertMember(t, s, back)
}

func addMembersAgain(t *testing.T, s MemberRooms) {
	crew(t, s)
	j := domain.Join{Room: roomA, Tenant: tenant, RequestID: "req-add", By: "alice", At: secs(10)}
	first, err := s.AddMembers(t.Context(), j, []string{"carol"})
	if err != nil || first.Changed != 1 {
		t.Fatalf("AddMembers = %+v, %v; want 1 changed", first, err)
	}
	other := j
	other.RequestID, other.By, other.At = "req-other", "bob", secs(20)
	for _, again := range []domain.Join{j, other} {
		res, err := s.AddMembers(t.Context(), again, []string{"carol", "bob"})
		if err != nil || res.Changed != 0 {
			t.Fatalf("AddMembers(%s) again = %+v, %v; want nothing changed", again.RequestID, res, err)
		}
		assertMembers(t, "AddMembers again", res.Members[:1], first.Members)
	}
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "carol"), first.Members)
}

func addMembersInvalid(t *testing.T, s MemberRooms) {
	crew(t, s)
	good := domain.Join{Room: roomA, Tenant: tenant, RequestID: "req-add", By: "alice", At: secs(10)}
	noRequest, zeroTime := good, good
	noRequest.RequestID, zeroTime.At = "", time.Time{}
	for name, c := range map[string]struct {
		j     domain.Join
		users []string
	}{
		"no request id":  {noRequest, []string{"carol"}},
		"zero time":      {zeroTime, []string{"carol"}},
		"no users":       {good, nil},
		"duplicate user": {good, []string{"carol", "carol"}},
		"bad user":       {good, []string{"carol", "d e"}},
	} {
		_, err := s.AddMembers(t.Context(), c.j, c.users)
		assertErrorIs(t, "AddMembers("+name+")", err, apperr.ErrInvalidArgument)
	}
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "carol"), nil)
}
