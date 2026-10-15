package storetest

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type MemberStateStores struct {
	Rooms  MemberRooms
	States store.EventStates
}

func (s MemberStateStores) states() EventStateStores { return EventStateStores{States: s.States} }

type memberStateCase struct {
	name string
	run  func(t *testing.T, s MemberStateStores)
}

type flagTime func(time.Time) bool

func RunMemberStates(t *testing.T, open func(t *testing.T) MemberStateStores) {
	t.Helper()
	for _, c := range slices.Concat(memberStateCases(), memberChangeStateCases(), roomStateCases()) {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func memberStateCases() []memberStateCase {
	return []memberStateCase{
		{"create flags the room with bits 1 and 2 and each founder with bit 1 at the creation time", createFlagsRoomAndFounders},
		{"insert room flags bit 1, add members flags each new member and the count adds bit 2", insertJoinCountFlags},
		{"add members of active members flags nothing", addActiveMembersFlagsNothing},
	}
}

func createFlagsRoomAndFounders(t *testing.T, s MemberStateStores) {
	crew(t, s.Rooms)
	assertFlags(t, s, store.StateRooms, exactly(baseTime), map[string]store.StateBit{roomKey(roomA): store.BitRoomCreated | store.BitMemberCount})
	assertFlags(t, s, store.StateMembers, exactly(baseTime), map[string]store.StateBit{"alice": store.BitMember, "bob": store.BitMember})
	clearCrewFlags(t, s)
	room, members := teamOf(roomA)
	assertErrorIs(t, "Create(existing)", s.Rooms.Create(t.Context(), room, members), store.ErrRoomExists)
	assertNoPending(t, s.states(), store.StateRooms)
	assertNoPending(t, s.states(), store.StateMembers)
}

func insertJoinCountFlags(t *testing.T, s MemberStateStores) {
	mustInsertRoom(t, s.Rooms, group(roomA, "Team", 0))
	assertFlags(t, s, store.StateRooms, exactly(baseTime), map[string]store.StateBit{roomKey(roomA): store.BitRoomCreated})
	before := time.Now()
	j := domain.Join{Room: roomA, Tenant: tenant, RequestID: "req-add", By: "alice", At: secs(10)}
	if _, err := s.Rooms.AddMembers(t.Context(), j, []string{"alice", "bob"}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	if _, err := s.Rooms.AddMemberCount(t.Context(), roomA, 2); err != nil {
		t.Fatalf("AddMemberCount: %v", err)
	}
	assertFlags(t, s, store.StateMembers, during(before, time.Now()), map[string]store.StateBit{"alice": store.BitMember, "bob": store.BitMember})
	assertFlags(t, s, store.StateRooms, exactly(baseTime), map[string]store.StateBit{roomKey(roomA): store.BitRoomCreated | store.BitMemberCount})
}

func addActiveMembersFlagsNothing(t *testing.T, s MemberStateStores) {
	settledCrew(t, s)
	j := domain.Join{Room: roomA, Tenant: tenant, RequestID: "req-again", By: "alice", At: secs(10), ReadSeq: 9}
	res, err := s.Rooms.AddMembers(t.Context(), j, []string{"alice", "bob"})
	if err != nil || res.Changed != 0 {
		t.Fatalf("AddMembers(active) = %+v, %v; want no change", res, err)
	}
	assertNoPending(t, s.states(), store.StateMembers)
}

func settledCrew(t *testing.T, s MemberStateStores) (alice, bob domain.Member) {
	t.Helper()
	alice, bob = crew(t, s.Rooms)
	clearCrewFlags(t, s)
	return alice, bob
}

func clearCrewFlags(t *testing.T, s MemberStateStores) {
	t.Helper()
	mustClearStates(t, s.states(), roomState(roomA, store.BitRoomCreated, 0), roomState(roomA, store.BitMemberCount, 1),
		memberState("alice", store.BitMember, 1, time.Time{}), memberState("bob", store.BitMember, 1, time.Time{}))
	assertNoPending(t, s.states(), store.StateRooms)
	assertNoPending(t, s.states(), store.StateMembers)
}

func assertFlags(t *testing.T, s MemberStateStores, c store.StateCollection, at flagTime, want map[string]store.StateBit) {
	t.Helper()
	got := allPending(t, s.states(), c)
	if len(got) != len(want) {
		t.Fatalf("Pending(%s) = %+v, want %v", c, got, want)
	}
	for _, d := range got {
		key := d.User
		if c == store.StateRooms {
			key = roomKey(d.Room)
		}
		if bits, ok := want[key]; !ok || d.State != bits || (c == store.StateMembers && d.Room != roomA) || !at(d.StateAt) {
			t.Fatalf("Pending(%s) doc %+v, want %v with a matching flag time", c, d, want)
		}
	}
}

func exactly(want time.Time) flagTime { return want.Equal }

func during(before, after time.Time) flagTime {
	return func(at time.Time) bool { return aroundWrite(at, before, after) }
}

func roomKey(room uint64) string { return strconv.FormatUint(room, 10) }
