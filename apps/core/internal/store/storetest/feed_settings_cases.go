package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func memberFeedRoomInfoAndPreferences(t *testing.T, s MemberRooms, _ store.Hidden, cur store.Cursor) {
	crew(t, s)
	nextChanges(t, cur, 3)
	rename := store.RoomInfoChange{Update: domain.RoomUpdate{Name: new("Design")}, By: "alice", At: secs(5)}
	if !updateInfo(t, s, 1, rename) || updateInfo(t, s, 1, rename) {
		t.Fatal("UpdateRoomInfo(base 1) twice: want the first written and the second lost")
	}
	if _, err := s.AddMemberCount(t.Context(), roomA, 1); err != nil {
		t.Fatalf("AddMemberCount: %v", err)
	}
	pin := domain.PreferencesChange{Pinned: new(true)}
	for range 2 {
		if _, _, err := s.SetPreferences(t.Context(), roomA, "bob", pin, secs(6)); err != nil {
			t.Fatalf("SetPreferences: %v", err)
		}
	}
	mustMarkRead(t, s, "bob", 2)
	got := nextChanges(t, cur, 3)
	if c := got[0]; c.Kind != store.RoomChanged || c.Room != (domain.Room{ID: roomA, InfoVer: 2}) || c.Member.User != "" {
		t.Fatalf("room info change = %+v, want room %d at info ver 2 and nothing else", c, roomA)
	}
	assertMemberChange(t, got[1], store.PreferenceChanged, "bob", 1)
	assertMemberChange(t, got[2], store.ReadChanged, "bob", 1)
}
