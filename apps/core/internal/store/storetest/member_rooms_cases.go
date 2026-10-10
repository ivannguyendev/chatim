package storetest

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const roomC uint64 = 7_340_000_003

func memberRoomCases() []memberCase {
	return []memberCase{
		{"rooms of a user lists the rooms where the user is active in that tenant", roomsOfActive},
	}
}

func roomsOfActive(t *testing.T, s MemberRooms) {
	for _, id := range []uint64{roomB, roomA} {
		room, members := teamOf(id)
		mustCreate(t, s, room, members)
	}
	elsewhere := group(roomC, "Other", 1)
	elsewhere.Tenant = "globex"
	bob := member(roomC, "bob", domain.RoleOwner)
	bob.Tenant = "globex"
	mustCreate(t, s, elsewhere, []domain.Member{bob})
	removeMember(t, s, docOf(t, s, roomB, "bob"), secs(5))
	for user, want := range map[string][]uint64{"alice": {roomA, roomB}, "bob": {roomA}, "nobody": {}} {
		got, err := s.RoomsOf(t.Context(), tenant, user)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("RoomsOf(%q) = %v, %v; want %v", user, got, err, want)
		}
	}
	if got, err := s.RoomsOf(t.Context(), "globex", "bob"); err != nil || !slices.Equal(got, []uint64{roomC}) {
		t.Fatalf("RoomsOf(globex, bob) = %v, %v; want [%d]", got, err, roomC)
	}
	if _, err := s.RoomsOf(t.Context(), tenant, ""); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("RoomsOf(empty user) = %v, want ErrInvalidArgument", err)
	}
}
