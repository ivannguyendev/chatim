package mongostore

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	hiddenUniqueIndex = "user_id_1_room_id_1_thread_root_1_seq_1"
	extraRooms        = 40
)

var hiddenUsers = []string{"bob", "carolyn", boundedLong}

func seedHiddenAndRooms(t *testing.T, s *Store) {
	t.Helper()
	at := codecTime
	for _, room := range boundedRooms {
		for _, user := range hiddenUsers {
			for seq := uint64(1); seq <= 30; seq++ {
				if _, err := s.Hidden().Hide(t.Context(), user, store.MsgKey{Room: room, Seq: seq}, at); err != nil {
					t.Fatalf("Hide(%d/%d for %s): %v", room, seq, user, err)
				}
			}
		}
	}
	for i := range extraRooms {
		r := domain.Room{ID: boundedRoomC + 1 + uint64(i), Tenant: "acme", Type: domain.RoomGroup, Name: fmt.Sprintf("Extra %d", i), CreatedBy: "x", CreatedAt: codecTime}
		if err := s.InsertRoom(t.Context(), r); err != nil {
			t.Fatalf("InsertRoom(%d): %v", r.ID, err)
		}
	}
}

func flaggedHiddenAndRooms() ([]store.StateTarget, []store.StateTarget, []store.StateTarget) {
	hide := func(room, seq uint64, user string) store.StateTarget {
		return store.StateTarget{Collection: store.StateHidden, Bit: store.BitHidden, Room: room, Seq: seq, User: user}
	}
	hidden := []store.StateTarget{hide(boundedRoomA, 3, "bob"), hide(boundedRoomA, 3, "carolyn"), hide(boundedRoomA, 30, boundedLong), hide(boundedRoomB, 1, "bob"), hide(boundedRoomC, 17, "carolyn")}
	created := []store.StateTarget{
		{Collection: store.StateRooms, Bit: store.BitRoomCreated, Room: boundedRoomA},
		{Collection: store.StateRooms, Bit: store.BitRoomCreated, Room: boundedRoomC + 5},
		{Collection: store.StateRooms, Bit: store.BitRoomCreated, Room: boundedRoomC + extraRooms},
	}
	counts := []store.StateTarget{
		{Collection: store.StateRooms, Bit: store.BitMemberCount, Room: boundedRoomB, Ver: 4},
		{Collection: store.StateRooms, Bit: store.BitMemberCount, Room: boundedRoomC + 9, Ver: 2},
	}
	return hidden, created, counts
}

func TestClearAndMarkOnHiddenAndRoomsUseTheirKeys(t *testing.T) {
	s, db, _, updates := monitoredStateStore(t)
	seedBoundedLookups(t, s)
	seedHiddenAndRooms(t, s)
	unflagAll(t, db)
	hidden, created, counts := flaggedHiddenAndRooms()
	seedStates(t, db, slices.Concat(hidden, created, counts), time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC))
	expectPendingIndexKeys(t, db, len(hidden), hiddenCollection)
	expectPendingIndexKeys(t, db, len(created)+len(counts), roomsCollection)

	if err := s.EventStates().Clear(t.Context(), hidden); err != nil {
		t.Fatalf("Clear hidden: %v", err)
	}
	expectPendingIndexKeys(t, db, 0, hiddenCollection)
	expectKeyedUpdate(t, "Clear hidden", explainLastUpdate(t, db, updates, hiddenCollection), len(hidden), hiddenUniqueIndex)

	if err := s.EventStates().Clear(t.Context(), created); err != nil {
		t.Fatalf("Clear room created: %v", err)
	}
	expectPendingIndexKeys(t, db, len(counts), roomsCollection)
	expectKeyedUpdate(t, "Clear room created", explainLastUpdate(t, db, updates, roomsCollection), len(created), "_id_", "IDHACK")

	if err := s.EventStates().Clear(t.Context(), counts); err != nil {
		t.Fatalf("Clear member counts: %v", err)
	}
	expectPendingIndexKeys(t, db, 0, roomsCollection)
	expectKeyedUpdate(t, "Clear member counts", explainLastUpdate(t, db, updates, roomsCollection), len(counts), "_id_", "IDHACK")

	if err := s.EventStates().Mark(t.Context(), store.StateHidden, hidden); err != nil {
		t.Fatalf("Mark hidden: %v", err)
	}
	expectPendingIndexKeys(t, db, len(hidden), hiddenCollection)
	expectKeyedUpdate(t, "Mark hidden", explainLastUpdate(t, db, updates, hiddenCollection), len(hidden), hiddenUniqueIndex)

	if err := s.EventStates().Mark(t.Context(), store.StateRooms, counts); err != nil {
		t.Fatalf("Mark member counts: %v", err)
	}
	expectPendingIndexKeys(t, db, len(counts), roomsCollection)
	expectKeyedUpdate(t, "Mark member counts", explainLastUpdate(t, db, updates, roomsCollection), len(counts), "_id_", "IDHACK")
}

func expectKeyedUpdate(t *testing.T, name string, c lookupCost, keys int, anyOf ...string) {
	t.Helper()
	expectBoundedUpdate(t, name, c, keys)
	for _, want := range anyOf {
		if strings.Contains(c.plan, want) {
			return
		}
	}
	t.Errorf("%s: plan %s; want it to use one of %v", name, c.plan, anyOf)
}
