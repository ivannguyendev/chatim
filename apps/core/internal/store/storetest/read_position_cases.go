package storetest

import (
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func readPositionCases() []memberCase {
	return []memberCase{
		{"mark read only raises and mark unread only lowers, one read ver per change", readMoves},
		{"read positions never touch the membership ver", readKeepsVer},
		{"read positions and clear history need an active member", readNeedsActive},
		{"mark read rejects a seq past int64", readInvalid},
	}
}

func mark(t *testing.T, s MemberRooms, unread bool, seq uint64) (domain.ReadPosition, bool) {
	t.Helper()
	call := s.MarkRead
	if unread {
		call = s.MarkUnread
	}
	pos, changed, err := call(t.Context(), roomA, "bob", seq)
	if err != nil {
		t.Fatalf("mark(unread %v, %d): %v", unread, seq, err)
	}
	return pos, changed
}

func readMoves(t *testing.T, s MemberRooms) {
	crew(t, s)
	steps := []struct {
		unread  bool
		seq     uint64
		want    domain.ReadPosition
		changed bool
	}{
		{false, 5, domain.ReadPosition{Seq: 5, Ver: 1}, true},
		{false, 3, domain.ReadPosition{Seq: 5, Ver: 1}, false},
		{false, 5, domain.ReadPosition{Seq: 5, Ver: 1}, false},
		{true, 2, domain.ReadPosition{Seq: 2, Ver: 2}, true},
		{true, 4, domain.ReadPosition{Seq: 2, Ver: 2}, false},
		{true, 2, domain.ReadPosition{Seq: 2, Ver: 2}, false},
		{false, 9, domain.ReadPosition{Seq: 9, Ver: 3}, true},
	}
	for _, st := range steps {
		before := docOf(t, s, roomA, "bob")
		pos, changed := mark(t, s, st.unread, st.seq)
		if pos != st.want || changed != st.changed {
			t.Fatalf("mark(unread %v, %d) = %+v, %v; want %+v, %v", st.unread, st.seq, pos, changed, st.want, st.changed)
		}
		after := docOf(t, s, roomA, "bob")
		stamped := after.LastChangeAt.After(baseTime) && !after.LastChangeAt.Before(before.LastChangeAt)
		kept := after.LastChangeAt.Equal(before.LastChangeAt)
		if after.ReadSeq != st.want.Seq || after.ReadVer != st.want.Ver || (st.changed && !stamped) || (!st.changed && !kept) {
			t.Fatalf("doc after mark(unread %v, %d) = %+v, before %+v", st.unread, st.seq, after, before)
		}
	}
}

func readKeepsVer(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	mark(t, s, false, 5)
	mark(t, s, true, 1)
	mark(t, s, false, 8)
	after := docOf(t, s, roomA, "bob")
	if after.Ver != bob.Ver || !after.UpdatedAt.Equal(bob.UpdatedAt) || after.UpdatedBy != bob.UpdatedBy || after.RequestID != bob.RequestID {
		t.Fatalf("doc after three read moves = %+v, want ver and audit of %+v", after, bob)
	}
	admin := bob.Next(domain.RoleAdmin, domain.MemberActive, 0, "req-up", "alice", secs(5))
	mustApplyMember(t, s, bob, admin)
	want := admin
	want.ReadSeq, want.ReadVer = 8, 3
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob"), []domain.Member{want})
}

func readNeedsActive(t *testing.T, s MemberRooms) {
	_, bob := crew(t, s)
	gone := removeMember(t, s, bob, secs(5))
	for user, room := range map[string]uint64{"bob": roomA, "carol": roomA, "alice": roomB} {
		_, _, err := s.MarkRead(t.Context(), room, user, 3)
		assertErrorIs(t, "MarkRead("+user+")", err, domain.ErrNotMember)
		_, _, err = s.MarkUnread(t.Context(), room, user, 0)
		assertErrorIs(t, "MarkUnread("+user+")", err, domain.ErrNotMember)
		_, _, err = s.ClearHistory(t.Context(), room, user, secs(6))
		assertErrorIs(t, "ClearHistory("+user+")", err, domain.ErrNotMember)
	}
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob"), []domain.Member{gone})
}

func readInvalid(t *testing.T, s MemberRooms) {
	crew(t, s)
	_, _, err := s.MarkRead(t.Context(), roomA, "bob", math.MaxInt64+1)
	assertErrorIs(t, "MarkRead(past int64)", err, apperr.ErrInvalidArgument)
	_, _, err = s.MarkUnread(t.Context(), roomA, "bob", math.MaxInt64+1)
	assertErrorIs(t, "MarkUnread(past int64)", err, apperr.ErrInvalidArgument)
	if got := docOf(t, s, roomA, "bob"); got.ReadSeq != 0 || got.ReadVer != 0 {
		t.Fatalf("doc after rejected marks = %+v, want read seq 0 ver 0", got)
	}
}
