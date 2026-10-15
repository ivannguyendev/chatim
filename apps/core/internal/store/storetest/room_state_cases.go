package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func roomStateCases() []memberStateCase {
	return []memberStateCase{
		{"update room info flags bit 4 and a stale base flags nothing", roomInfoFlags},
		{"member count writes flag bit 2 and a lost CAS or a missing room flags nothing", memberCountFlags},
	}
}

func roomInfoFlags(t *testing.T, s MemberStateStores) {
	settledCrew(t, s)
	before := time.Now()
	rename := store.RoomInfoChange{Update: domain.RoomUpdate{Name: new("Design")}, By: "alice", At: secs(5)}
	if ok, err := s.Rooms.UpdateRoomInfo(t.Context(), roomA, 1, rename); err != nil || !ok {
		t.Fatalf("UpdateRoomInfo(base 1) = %v, %v; want written", ok, err)
	}
	assertFlags(t, s, store.StateRooms, during(before, time.Now()), map[string]store.StateBit{roomKey(roomA): store.BitRoomInfo})
	mustClearStates(t, s.states(), roomState(roomA, store.BitRoomInfo, 2))
	if ok, err := s.Rooms.UpdateRoomInfo(t.Context(), roomA, 1, rename); err != nil || ok {
		t.Fatalf("UpdateRoomInfo(stale base) = %v, %v; want false", ok, err)
	}
	assertNoPending(t, s.states(), store.StateRooms)
}

func memberCountFlags(t *testing.T, s MemberStateStores) {
	settledCrew(t, s)
	before := time.Now()
	if got, err := s.Rooms.AddMemberCount(t.Context(), roomA, 1); err != nil || got.Ver != 2 {
		t.Fatalf("AddMemberCount = %+v, %v; want ver 2", got, err)
	}
	assertFlags(t, s, store.StateRooms, during(before, time.Now()), map[string]store.StateBit{roomKey(roomA): store.BitMemberCount})
	mustClearStates(t, s.states(), roomState(roomA, store.BitMemberCount, 2))
	before = time.Now()
	if got, ok, err := s.Rooms.SetMemberCount(t.Context(), roomA, 2, 5); err != nil || !ok || got.Ver != 3 {
		t.Fatalf("SetMemberCount(base 2) = %+v, %v, %v; want ver 3", got, ok, err)
	}
	assertFlags(t, s, store.StateRooms, during(before, time.Now()), map[string]store.StateBit{roomKey(roomA): store.BitMemberCount})
	mustClearStates(t, s.states(), roomState(roomA, store.BitMemberCount, 3))
	if _, ok, err := s.Rooms.SetMemberCount(t.Context(), roomA, 2, 7); err != nil || ok {
		t.Fatalf("SetMemberCount(stale base) = %v, %v; want false", ok, err)
	}
	_, err := s.Rooms.AddMemberCount(t.Context(), roomB, 1)
	assertErrorIs(t, "AddMemberCount(missing room)", err, domain.ErrRoomNotFound)
	assertNoPending(t, s.states(), store.StateRooms)
}
