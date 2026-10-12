package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestParseRoleKnowsThreeRoles(t *testing.T) {
	for _, r := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleMember} {
		got, err := domain.ParseRole(string(r))
		if err != nil || got != r {
			t.Fatalf("ParseRole(%q) = %q, %v", r, got, err)
		}
	}
	if domain.RoleAdmin != "admin" {
		t.Fatalf("RoleAdmin = %q, want admin", domain.RoleAdmin)
	}
	for _, s := range []string{"", "Owner", "guest"} {
		_, err := domain.ParseRole(s)
		if !errors.Is(err, apperr.ErrInvalidArgument) || err.Error() != apperr.ErrInvalidArgument.Error()+": role" {
			t.Fatalf("ParseRole(%q) = %v, want invalid role", s, err)
		}
	}
}

func TestCreationRequestIDIsAValidCID(t *testing.T) {
	for room, want := range map[uint64]string{1: "1-created", 18446744073709551615: "18446744073709551615-created"} {
		got := domain.CreationRequestID(room)
		if got != want || domain.ValidCID(got) != nil {
			t.Fatalf("CreationRequestID(%d) = %q (valid %v), want %q", room, got, domain.ValidCID(got), want)
		}
	}
}

func TestNextMovesMembershipAndKeepsReaderState(t *testing.T) {
	joined := time.Unix(100, 0)
	cleared := time.Unix(200, 0)
	at := time.Unix(300, 0)
	cur := domain.Member{
		Room: 7, Tenant: "acme", User: "bob", Role: domain.RoleAdmin, JoinedAt: joined, ClearedAt: cleared,
		State: domain.MemberActive, Ver: 4, Priority: 9, RequestID: "r1", UpdatedAt: joined, UpdatedBy: "alice",
		LastChangeAt: cleared, ReadSeq: 12, ReadVer: 3,
	}
	got := cur.Next(domain.RoleMember, domain.MemberRemoved, 2, "r2", "carol", at)
	want := domain.Member{
		Room: 7, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: joined, ClearedAt: cleared,
		State: domain.MemberRemoved, Ver: 5, Priority: 2,
		PreviousRole: domain.RoleAdmin, PreviousState: domain.MemberActive, PreviousPriority: 9,
		RequestID: "r2", UpdatedAt: at, UpdatedBy: "carol", LastChangeAt: at, ReadSeq: 12, ReadVer: 3,
	}
	if got != want {
		t.Fatalf("Next = %+v, want %+v", got, want)
	}
	if cur.Ver != 4 || cur.Role != domain.RoleAdmin {
		t.Fatalf("Next changed its receiver: %+v", cur)
	}
	if !cur.Active() || got.Active() {
		t.Fatalf("Active() = %v, %v; want true, false", cur.Active(), got.Active())
	}
}

func TestJoinApplyAddsReAddsAndKeepsActiveMembers(t *testing.T) {
	at := time.Unix(500, 0)
	j := domain.Join{Room: 7, Tenant: "acme", RequestID: "req-1", By: "alice", At: at, ReadSeq: 40}

	fresh := j.Apply(domain.Member{}, "bob")
	wantFresh := domain.Member{
		Room: 7, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: at, State: domain.MemberActive,
		Ver: 1, RequestID: "req-1", UpdatedAt: at, UpdatedBy: "alice", LastChangeAt: at, ReadSeq: 40, ReadVer: 1,
	}
	if fresh != wantFresh {
		t.Fatalf("Apply(no doc) = %+v, want %+v", fresh, wantFresh)
	}

	cleared := time.Unix(50, 0)
	tomb := domain.Member{
		Room: 7, Tenant: "acme", User: "dan", Role: domain.RoleAdmin, JoinedAt: time.Unix(10, 0), ClearedAt: cleared,
		State: domain.MemberRemoved, Ver: 6, Priority: 3, PreviousRole: domain.RoleAdmin, PreviousState: domain.MemberActive,
		RequestID: "old", UpdatedAt: time.Unix(60, 0), UpdatedBy: "dan", LastChangeAt: time.Unix(60, 0), ReadSeq: 90, ReadVer: 8,
	}
	back := j.Apply(tomb, "dan")
	wantBack := domain.Member{
		Room: 7, Tenant: "acme", User: "dan", Role: domain.RoleMember, JoinedAt: at, ClearedAt: cleared,
		State: domain.MemberActive, Ver: 7, Priority: 0,
		PreviousRole: domain.RoleAdmin, PreviousState: domain.MemberRemoved, PreviousPriority: 3,
		RequestID: "req-1", UpdatedAt: at, UpdatedBy: "alice", LastChangeAt: at, ReadSeq: 90, ReadVer: 9,
	}
	if back != wantBack {
		t.Fatalf("Apply(tombstone) = %+v, want %+v", back, wantBack)
	}

	active := wantBack
	active.RequestID = "other"
	if got := j.Apply(active, "dan"); got != active {
		t.Fatalf("Apply(active) = %+v, want it unchanged", got)
	}
}

func TestAddedByNeedsActiveSameRequestAndCaller(t *testing.T) {
	m := domain.Member{State: domain.MemberActive, RequestID: "req-1", UpdatedBy: "alice"}
	if !domain.AddedBy(m, "req-1", "alice") {
		t.Fatal("AddedBy(active, same request, same caller) = false")
	}
	removed := m
	removed.State = domain.MemberRemoved
	for name, c := range map[string]struct {
		m       domain.Member
		req, by string
	}{
		"removed":       {removed, "req-1", "alice"},
		"other request": {m, "req-2", "alice"},
		"other caller":  {m, "req-1", "bob"},
	} {
		if domain.AddedBy(c.m, c.req, c.by) {
			t.Errorf("AddedBy(%s) = true", name)
		}
	}
}

func TestMemberValueTypesHoldTheirFields(t *testing.T) {
	if domain.MaxMemberBatch != 1000 || domain.MemberActive != 1 || domain.MemberRemoved != 2 {
		t.Fatalf("constants = %d %d %d", domain.MaxMemberBatch, domain.MemberActive, domain.MemberRemoved)
	}
	c := domain.MemberCount{Count: 3, Ver: 2}
	p := domain.ReadPosition{Seq: 9, Ver: 4}
	if c != (domain.MemberCount{Count: 3, Ver: 2}) || p != (domain.ReadPosition{Seq: 9, Ver: 4}) {
		t.Fatalf("value types = %+v %+v", c, p)
	}
	r := domain.Room{ID: 1, MemberCountVer: 5}
	if r != (domain.Room{ID: 1, MemberCountVer: 5}) {
		t.Fatalf("Room = %+v", r)
	}
}
