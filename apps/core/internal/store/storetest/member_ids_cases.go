package storetest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func memberIDCases() []memberCase {
	return []memberCase{
		{"member user ids list every active member of one room only", memberUserIDsOfRoom},
		{"member user ids reject a bad room", memberUserIDsInvalid},
	}
}

func memberUserIDsOfRoom(t *testing.T, s MemberRooms) {
	seedRankedMembers(t, s)
	got, err := s.MemberUserIDs(t.Context(), roomA)
	slices.Sort(got)
	want := []string{"alice", "amy", "bea", "bob", "carl", "dave", "zed"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("MemberUserIDs = %v, %v; want %v", got, err, want)
	}
	if got, err := s.MemberUserIDs(t.Context(), roomC); err != nil || len(got) != 0 {
		t.Fatalf("MemberUserIDs(empty room) = %v, %v; want none", got, err)
	}
}

func memberUserIDsInvalid(t *testing.T, s MemberRooms) {
	for _, room := range []uint64{0, 1 << 63} {
		_, err := s.MemberUserIDs(t.Context(), room)
		assertErrorIs(t, "MemberUserIDs", err, apperr.ErrInvalidArgument)
	}
}
