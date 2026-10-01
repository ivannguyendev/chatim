package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func roomsCases() []roomsCase {
	return []roomsCase{
		{"create then get room and members", roomsCreate},
		{"create of an existing id fails and keeps the room", roomsCreateExisting},
		{"get of a missing room is not found", roomsMissing},
		{"invalid room or members are rejected and not stored", roomsInvalid},
		{"membership is per room", roomsMembership},
		{"cancelled context", roomsCancelled},
	}
}

func group(id uint64, name string, members int) domain.Room {
	return domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: name, CreatedBy: "alice", CreatedAt: baseTime, MemberCount: members}
}

func member(room uint64, user string, role domain.Role) domain.Member {
	return domain.Member{Room: room, Tenant: tenant, User: user, Role: role, JoinedAt: baseTime}
}

func teamOf(room uint64) (domain.Room, []domain.Member) {
	return group(room, "Team", 2), []domain.Member{member(room, "alice", domain.RoleOwner), member(room, "bob", domain.RoleMember)}
}

func mustCreate(t *testing.T, s store.Rooms, r domain.Room, members []domain.Member) {
	t.Helper()
	if err := s.Create(t.Context(), r, members); err != nil {
		t.Fatalf("Create(%d): %v", r.ID, err)
	}
}

func assertRoom(t *testing.T, s store.Rooms, want domain.Room) {
	t.Helper()
	got, err := s.Get(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("Get(%d): %v", want.ID, err)
	}
	gotAt, wantAt := got.CreatedAt, want.CreatedAt
	got.CreatedAt, want.CreatedAt = time.Time{}, time.Time{}
	if got != want || !gotAt.Equal(wantAt) {
		t.Fatalf("Get(%d) = %+v at %v, want %+v at %v", want.ID, got, gotAt, want, wantAt)
	}
}

func assertMember(t *testing.T, s store.Rooms, want domain.Member) {
	t.Helper()
	got, err := s.Member(t.Context(), want.Room, want.User)
	if err != nil {
		t.Fatalf("Member(%d, %q): %v", want.Room, want.User, err)
	}
	gotAt, wantAt := got.JoinedAt, want.JoinedAt
	got.JoinedAt, want.JoinedAt = time.Time{}, time.Time{}
	if got != want || !gotAt.Equal(wantAt) {
		t.Fatalf("Member(%d, %q) = %+v at %v, want %+v at %v", want.Room, want.User, got, gotAt, want, wantAt)
	}
}

func assertNotMember(t *testing.T, s store.Rooms, room uint64, user string) {
	t.Helper()
	_, err := s.Member(t.Context(), room, user)
	assertErrorIs(t, "Member", err, domain.ErrNotMember)
}

func assertNoRoom(t *testing.T, s store.Rooms, id uint64) {
	t.Helper()
	_, err := s.Get(t.Context(), id)
	assertErrorIs(t, "Get", err, domain.ErrRoomNotFound)
}

func roomsCreate(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	assertRoom(t, s, room)
	for _, m := range members {
		assertMember(t, s, m)
	}
}

func roomsCreateExisting(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	again := group(roomA, "Other", 2)
	againMembers := []domain.Member{member(roomA, "alice", domain.RoleMember), member(roomA, "dave", domain.RoleOwner)}
	err := s.Create(t.Context(), again, againMembers)
	assertErrorIs(t, "Create", err, apperr.ErrAlreadyExists)
	assertRoom(t, s, room)
	for _, m := range members {
		assertMember(t, s, m)
	}
}

func roomsMissing(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	assertNoRoom(t, s, roomB)
	assertNoRoom(t, s, 0)
}

func roomsInvalid(t *testing.T, s store.Rooms) {
	noTenant, noTenantOwner := group(roomA, "Team", 1), member(roomA, "alice", domain.RoleOwner)
	noTenant.Tenant, noTenantOwner.Tenant = "", ""
	foreign := member(roomA+4, "bob", domain.RoleMember)
	foreign.Tenant = "other"
	tests := []struct {
		name    string
		room    domain.Room
		members []domain.Member
	}{
		{"zero room id", group(0, "Team", 1), []domain.Member{member(0, "alice", domain.RoleOwner)}},
		{"empty tenant", noTenant, []domain.Member{noTenantOwner}},
		{"no members", group(roomA+1, "Team", 0), nil},
		{"member of another room", group(roomA+2, "Team", 2), []domain.Member{
			member(roomA+2, "alice", domain.RoleOwner), member(roomA+3, "bob", domain.RoleMember),
		}},
		{"member of another tenant", group(roomA+4, "Team", 2), []domain.Member{
			member(roomA+4, "alice", domain.RoleOwner), foreign,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertErrorIs(t, "Create", s.Create(t.Context(), tt.room, tt.members), apperr.ErrInvalidArgument)
			assertNoRoom(t, s, tt.room.ID)
			assertNotMember(t, s, tt.room.ID, "alice")
			for _, m := range tt.members {
				assertNotMember(t, s, m.Room, m.User)
			}
		})
	}
}

func roomsMembership(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	assertNotMember(t, s, roomA, "carol")
	assertNotMember(t, s, roomB, "alice")
	mustCreate(t, s, group(roomB, "Other", 1), []domain.Member{member(roomB, "carol", domain.RoleOwner)})
	assertMember(t, s, member(roomB, "carol", domain.RoleOwner))
	assertNotMember(t, s, roomB, "alice")
	assertNotMember(t, s, roomA, "carol")
	assertNotMember(t, s, roomA, "Alice")
}

func roomsCancelled(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	ctx := cancelledContext(t)
	assertErrorIs(t, "Create", s.Create(ctx, room, members), context.Canceled)
	assertNoRoom(t, s, roomA)
	assertNotMember(t, s, roomA, "alice")
	mustCreate(t, s, room, members)
	_, err := s.Get(ctx, roomA)
	assertErrorIs(t, "Get", err, context.Canceled)
	_, err = s.Member(ctx, roomA, "alice")
	assertErrorIs(t, "Member", err, context.Canceled)
}
