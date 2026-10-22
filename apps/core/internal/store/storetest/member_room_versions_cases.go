package storetest

import (
	"cmp"
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const manyRoomsBase uint64 = 7_350_000_000

func memberRoomVersionCases() []memberCase {
	return []memberCase{
		{"room versions of a user with no membership are empty", roomVersionsEmpty},
		{"room versions of a user list every active room once", roomVersionsEveryRoom},
		{"room versions of a user split active and left rooms and skip other tenants", roomVersionsActiveAndLeft},
		{"room versions of a user carry the membership version after leave and rejoin", roomVersionsRejoin},
		{"room versions of a user reject a bad user", roomVersionsInvalid},
	}
}

func roomVersionsEmpty(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	got, err := s.RoomVersionsOf(t.Context(), tenant, "nobody")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("RoomVersionsOf(nobody) = %#v, %v; want an empty list", got, err)
	}
}

func roomVersionsEveryRoom(t *testing.T, s MemberRooms) {
	var want []store.RoomVersion
	for i := range 30 {
		id := manyRoomsBase + uint64((i*7)%30) + 1
		room, members := teamOf(id)
		mustCreate(t, s, room, members)
		want = append(want, store.RoomVersion{Room: id, Ver: 1})
	}
	if got := roomVersionsOf(t, s, tenant, "bob"); !slices.Equal(got, byLeftThenRoom(want)) {
		t.Fatalf("RoomVersionsOf(bob) = %v,\nwant %v", got, byLeftThenRoom(want))
	}
}

func roomVersionsActiveAndLeft(t *testing.T, s MemberRooms) {
	for _, id := range []uint64{roomB, roomA} {
		room, members := teamOf(id)
		mustCreate(t, s, room, members)
	}
	elsewhere := group(roomC, "Other", 1)
	elsewhere.Tenant = "globex"
	bob := member(roomC, "bob", domain.RoleOwner)
	bob.Tenant = "globex"
	mustCreate(t, s, elsewhere, []domain.Member{bob})
	gone := removeMember(t, s, docOf(t, s, roomA, "bob"), secs(5))
	want := []store.RoomVersion{{Room: roomB, Ver: 1}, {Room: roomA, Ver: gone.Ver, Left: true}}
	if got := roomVersionsOf(t, s, tenant, "bob"); !slices.Equal(got, want) || gone.Ver != 2 {
		t.Fatalf("RoomVersionsOf(acme, bob) = %v; want %v with room A left at ver 2", got, want)
	}
	if got := roomVersionsOf(t, s, "globex", "bob"); !slices.Equal(got, []store.RoomVersion{{Room: roomC, Ver: 1}}) {
		t.Fatalf("RoomVersionsOf(globex, bob) = %v; want only room C at ver 1", got)
	}
	if got := roomVersionsOf(t, s, "initech", "bob"); len(got) != 0 {
		t.Fatalf("RoomVersionsOf(initech, bob) = %v; want none", got)
	}
}

func roomVersionsRejoin(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	gone := removeMember(t, s, bob, secs(5))
	if got := roomVersionsOf(t, s, tenant, "bob"); !slices.Equal(got, []store.RoomVersion{{Room: roomA, Ver: gone.Ver, Left: true}}) {
		t.Fatalf("RoomVersionsOf(bob) after leaving = %v; want room A left at ver %d", got, gone.Ver)
	}
	j := domain.Join{Room: roomA, Tenant: tenant, RequestID: "req-back", By: "alice", At: secs(10)}
	if res, err := s.AddMembers(t.Context(), j, []string{"bob"}); err != nil || res.Changed != 1 {
		t.Fatalf("AddMembers(bob) = %+v, %v; want rejoined", res, err)
	}
	back := docOf(t, s, roomA, "bob")
	want := []store.RoomVersion{{Room: roomA, Ver: back.Ver}}
	if got := roomVersionsOf(t, s, tenant, "bob"); !slices.Equal(got, want) || back.Ver != gone.Ver+1 || back.Ver != 3 {
		t.Fatalf("RoomVersionsOf(bob) after rejoin = %v; want %v at ver 3", got, want)
	}
}

func roomVersionsInvalid(t *testing.T, s MemberRooms) {
	if _, err := s.RoomVersionsOf(t.Context(), tenant, ""); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("RoomVersionsOf(empty user) = %v, want ErrInvalidArgument", err)
	}
}

func roomVersionsOf(t *testing.T, s MemberRooms, tenant, user string) []store.RoomVersion {
	t.Helper()
	got, err := s.RoomVersionsOf(t.Context(), tenant, user)
	if err != nil {
		t.Fatalf("RoomVersionsOf(%q, %q): %v", tenant, user, err)
	}
	return byLeftThenRoom(got)
}

func byLeftThenRoom(rooms []store.RoomVersion) []store.RoomVersion {
	out := slices.Clone(rooms)
	slices.SortFunc(out, func(a, b store.RoomVersion) int {
		if a.Left != b.Left {
			if a.Left {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.Room, b.Room)
	})
	return out
}
