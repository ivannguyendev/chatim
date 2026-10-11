package storetest

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const roomC uint64 = 7_340_000_003

func memberRoomCases() []memberCase {
	return []memberCase{
		{"rooms of a user lists the rooms where the user is active in that tenant", roomsOfActive},
		{"rooms of a user carries the join time, the clear time and the read position", roomsOfPositions},
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
		if got := roomIDsOf(t, s, tenant, user); !slices.Equal(got, want) {
			t.Fatalf("RoomsOf(%q) = %v; want %v", user, got, want)
		}
	}
	if got := roomIDsOf(t, s, "globex", "bob"); !slices.Equal(got, []uint64{roomC}) {
		t.Fatalf("RoomsOf(globex, bob) = %v; want [%d]", got, roomC)
	}
	if _, err := s.RoomsOf(t.Context(), tenant, ""); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("RoomsOf(empty user) = %v, want ErrInvalidArgument", err)
	}
}

func roomsOfPositions(t *testing.T, s MemberRooms) {
	for _, id := range []uint64{roomA, roomB} {
		room, members := teamOf(id)
		mustCreate(t, s, room, members)
	}
	if _, _, err := s.MarkRead(t.Context(), roomA, "alice", 7); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if _, _, err := s.ClearHistory(t.Context(), roomB, "alice", secs(30)); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	got, err := s.RoomsOf(t.Context(), tenant, "alice")
	if err != nil {
		t.Fatalf("RoomsOf: %v", err)
	}
	var want []store.MemberRoom
	for _, id := range []uint64{roomA, roomB} {
		d := docOf(t, s, id, "alice")
		want = append(want, store.MemberRoom{Room: id, JoinedAt: d.JoinedAt, ClearedAt: d.ClearedAt, ReadSeq: d.ReadSeq})
	}
	if !slices.EqualFunc(got, want, sameMemberRoom) || want[0].ReadSeq != 7 || !want[1].ClearedAt.Equal(secs(30)) || want[0].JoinedAt.IsZero() {
		t.Fatalf("RoomsOf(alice) = %+v,\nwant %+v with read seq 7 in A and a clear at %v in B", got, want, secs(30))
	}
}

func roomIDsOf(t *testing.T, s MemberRooms, tenant, user string) []uint64 {
	t.Helper()
	got, err := s.RoomsOf(t.Context(), tenant, user)
	if err != nil {
		t.Fatalf("RoomsOf(%q, %q): %v", tenant, user, err)
	}
	out := make([]uint64, len(got))
	for i, r := range got {
		out[i] = r.Room
	}
	return out
}

func sameMemberRoom(a, b store.MemberRoom) bool {
	return a.Room == b.Room && a.ReadSeq == b.ReadSeq && a.JoinedAt.Equal(b.JoinedAt) && a.ClearedAt.Equal(b.ClearedAt)
}
