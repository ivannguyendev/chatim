package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func memberChangeStateCases() []memberStateCase {
	return []memberStateCase{
		{"apply member flags bit 1 and a stale or missing doc flags nothing", applyMemberFlags},
		{"a committed owner change flags every written member and the room count", ownerCommitFlags},
		{"an owner change of a role flags the member and not the room", ownerRoleFlagsOnlyTheMember},
		{"an aborted owner change flags nothing", ownerAbortFlagsNothing},
		{"read moves flag bit 2 only when the position moves and a clear of an older read ver keeps it", readFlags},
		{"clear history flags bit 4 only when cleared at rises", clearHistoryFlags},
		{"set preferences flags bit 8 only on a change", preferenceFlags},
	}
}

func applyMemberFlags(t *testing.T, s MemberStateStores) {
	_, bob := settledCrew(t, s)
	before := time.Now()
	admin := changeMember(t, s.Rooms, bob, domain.RoleAdmin, domain.MemberActive, 3, secs(5))
	assertFlags(t, s, store.StateMembers, during(before, time.Now()), map[string]store.StateBit{"bob": store.BitMember})
	mustClearStates(t, s.states(), memberState("bob", store.BitMember, uint64(admin.Ver), time.Time{}))
	if ok, err := s.Rooms.ApplyMember(t.Context(), bob, bob.Next(domain.RoleOwner, domain.MemberActive, 0, "req-late", "alice", secs(6))); ok || err != nil {
		t.Fatalf("ApplyMember(stale) = %v, %v; want false", ok, err)
	}
	ghost := bob
	ghost.User = "ghost"
	if ok, err := s.Rooms.ApplyMember(t.Context(), ghost, ghost.Next(domain.RoleAdmin, domain.MemberActive, 0, "req-g", "alice", secs(6))); ok || err != nil {
		t.Fatalf("ApplyMember(no doc) = %v, %v; want false", ok, err)
	}
	assertNoPending(t, s.states(), store.StateMembers)
}

func ownerCommitFlags(t *testing.T, s MemberStateStores) {
	alice, bob := settledCrew(t, s)
	before := time.Now()
	up, out := step(bob, domain.RoleOwner, domain.MemberActive), step(alice, domain.RoleOwner, domain.MemberRemoved)
	res, err := s.Rooms.ChangeOwners(t.Context(), roomA, []string{"alice", "bob"}, plan(up, out))
	if err != nil || !res.CountChanged || res.Count.Ver != 2 {
		t.Fatalf("ChangeOwners = %+v, %v; want the count at ver 2", res, err)
	}
	after := time.Now()
	assertFlags(t, s, store.StateMembers, during(before, after), map[string]store.StateBit{"alice": store.BitMember, "bob": store.BitMember})
	assertFlags(t, s, store.StateRooms, during(before, after), map[string]store.StateBit{roomKey(roomA): store.BitMemberCount})
}

func ownerRoleFlagsOnlyTheMember(t *testing.T, s MemberStateStores) {
	_, bob := settledCrew(t, s)
	before := time.Now()
	if _, err := s.Rooms.ChangeOwners(t.Context(), roomA, []string{"bob"}, plan(step(bob, domain.RoleOwner, domain.MemberActive))); err != nil {
		t.Fatalf("ChangeOwners(role): %v", err)
	}
	assertFlags(t, s, store.StateMembers, during(before, time.Now()), map[string]store.StateBit{"bob": store.BitMember})
	assertNoPending(t, s.states(), store.StateRooms)
}

func ownerAbortFlagsNothing(t *testing.T, s MemberStateStores) {
	alice, bob := settledCrew(t, s)
	moved := changeMember(t, s.Rooms, alice, domain.RoleOwner, domain.MemberActive, 4, secs(5))
	mustClearStates(t, s.states(), memberState("alice", store.BitMember, uint64(moved.Ver), time.Time{}))
	stale := step(alice, domain.RoleOwner, domain.MemberRemoved)
	_, err := s.Rooms.ChangeOwners(t.Context(), roomA, []string{"bob", "alice"}, plan(step(bob, domain.RoleOwner, domain.MemberActive), stale))
	assertErrorIs(t, "ChangeOwners(stale second write)", err, domain.ErrRetryLater)
	assertNoPending(t, s.states(), store.StateMembers)
	assertNoPending(t, s.states(), store.StateRooms)
}

func readFlags(t *testing.T, s MemberStateStores) {
	settledCrew(t, s)
	for seq := uint64(1); seq <= 6; seq++ {
		mustMoveRead(t, s.Rooms.MarkRead, seq, seq, true)
	}
	mustClearStates(t, s.states(), readState(6))
	before := time.Now()
	mustMoveRead(t, s.Rooms.MarkRead, 40, 7, true)
	mustMoveRead(t, s.Rooms.MarkRead, 45, 8, true)
	mustClearStates(t, s.states(), readState(7))
	assertFlags(t, s, store.StateMembers, during(before, time.Now()), map[string]store.StateBit{"bob": store.BitReadPosition})
	mustClearStates(t, s.states(), readState(8))
	mustMoveRead(t, s.Rooms.MarkRead, 45, 8, false)
	mustMoveRead(t, s.Rooms.MarkUnread, 50, 8, false)
	assertNoPending(t, s.states(), store.StateMembers)
	mustMoveRead(t, s.Rooms.MarkUnread, 30, 9, true)
	assertFlags(t, s, store.StateMembers, during(before, time.Now()), map[string]store.StateBit{"bob": store.BitReadPosition})
}

type readMove func(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPosition, bool, error)

func mustMoveRead(t *testing.T, move readMove, seq, wantVer uint64, wantMoved bool) {
	t.Helper()
	pos, moved, err := move(t.Context(), roomA, "bob", seq)
	if err != nil || moved != wantMoved || pos.Ver != wantVer {
		t.Fatalf("move read of bob to %d = %+v, %v, %v; want ver %d moved %v", seq, pos, moved, err, wantVer, wantMoved)
	}
}

func readState(ver uint64) store.StateTarget {
	return memberState("bob", store.BitReadPosition, ver, time.Time{})
}

func clearHistoryFlags(t *testing.T, s MemberStateStores) {
	settledCrew(t, s)
	before := time.Now()
	if at, ok, err := s.Rooms.ClearHistory(t.Context(), roomA, "bob", secs(5)); err != nil || !ok || !at.Equal(secs(5)) {
		t.Fatalf("ClearHistory = %v, %v, %v; want raised to %v", at, ok, err, secs(5))
	}
	assertFlags(t, s, store.StateMembers, during(before, time.Now()), map[string]store.StateBit{"bob": store.BitHistoryCleared})
	mustClearStates(t, s.states(), memberState("bob", store.BitHistoryCleared, 0, secs(5)))
	for _, at := range []time.Time{secs(5), secs(3)} {
		if _, ok, err := s.Rooms.ClearHistory(t.Context(), roomA, "bob", at); err != nil || ok {
			t.Fatalf("ClearHistory(%v) = %v, %v; want no change", at, ok, err)
		}
	}
	assertNoPending(t, s.states(), store.StateMembers)
}

func preferenceFlags(t *testing.T, s MemberStateStores) {
	settledCrew(t, s)
	before := time.Now()
	pin := domain.PreferencesChange{Pinned: new(true)}
	if got, ok, err := s.Rooms.SetPreferences(t.Context(), roomA, "bob", pin, secs(5)); err != nil || !ok {
		t.Fatalf("SetPreferences = %+v, %v, %v; want changed", got, ok, err)
	}
	assertFlags(t, s, store.StateMembers, during(before, time.Now()), map[string]store.StateBit{"bob": store.BitPreferences})
	mustClearStates(t, s.states(), memberState("bob", store.BitPreferences, 1, time.Time{}))
	if _, ok, err := s.Rooms.SetPreferences(t.Context(), roomA, "bob", pin, secs(6)); err != nil || ok {
		t.Fatalf("SetPreferences(same) = %v, %v; want no change", ok, err)
	}
	assertNoPending(t, s.states(), store.StateMembers)
}
