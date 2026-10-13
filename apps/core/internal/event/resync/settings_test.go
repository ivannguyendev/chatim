package resync_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (w world) rename(t *testing.T, room uint64, at time.Time) {
	t.Helper()
	c := store.RoomInfoChange{Update: domain.RoomUpdate{Name: new("Kho")}, By: "alice", At: at}
	if ok, err := w.rooms.UpdateRoomInfo(t.Context(), room, 1, c); err != nil || !ok {
		t.Fatalf("UpdateRoomInfo(%d) = %v, %v", room, ok, err)
	}
}

func TestResyncReplaysRoomInfoAndPreferenceChangesOfTheLostRange(t *testing.T) {
	w := newWorld(t)
	w.rename(t, newRoom, lostFrom.Add(15*time.Minute))
	w.rename(t, busyRoom, lostTo.Add(time.Minute))
	pin := domain.PreferencesChange{Pinned: new(true)}
	if _, changed, err := w.rooms.SetPreferences(t.Context(), busyRoom, "alice", pin, lostFrom.Add(5*time.Minute)); err != nil || !changed {
		t.Fatalf("SetPreferences = %v, %v", changed, err)
	}
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := (resync.Report{Rooms: 2, RoomRecords: 1, RoomInfoRecords: 1, MessageRecords: 61, MemberRecords: 3}); rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	info := work.Record{Kind: store.RoomChanged, Room: newRoom, Version: 2}.ID()
	pref := work.Record{Kind: store.PreferenceChanged, Room: busyRoom, User: "alice", Version: 1}.ID()
	tail := []string{
		memberID(busyRoom, "alice", 1), pref,
		work.Record{Kind: store.RoomInserted, Room: newRoom}.ID(), info, memberID(newRoom, "alice", 1),
	}
	got := pub.published()
	if len(got) != 66 || !slices.Equal(got[61:], tail) {
		t.Fatalf("published %d ids ending %v, want 61 messages then %v", len(got), got[max(0, len(got)-5):], tail)
	}
	if slices.Contains(got, work.Record{Kind: store.RoomChanged, Room: busyRoom, Version: 2}.ID()) {
		t.Fatalf("published %v, want no info record for a rename after the range", got[61:])
	}
	recs := pub.records()
	if r := recs[64]; r.Kind != store.RoomChanged || !r.CommittedAt.Equal(lostFrom.Add(15*time.Minute)) {
		t.Fatalf("info record = %+v, want committed at the rename", r)
	}
}
