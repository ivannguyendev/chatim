package resync_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
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

func TestResyncMarksRoomInfoAndPreferenceChangesOfTheLostRange(t *testing.T) {
	w := newWorld(t)
	w.rename(t, newRoom, lostFrom.Add(15*time.Minute))
	w.rename(t, busyRoom, lostTo.Add(time.Minute))
	pin := domain.PreferencesChange{Pinned: new(true)}
	if _, changed, err := w.rooms.SetPreferences(t.Context(), busyRoom, "alice", pin, lostFrom.Add(5*time.Minute)); err != nil || !changed {
		t.Fatalf("SetPreferences = %v, %v", changed, err)
	}
	spy := w.spy()
	rep, err := resync.Run(t.Context(), w.deps(spy), resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := (resync.Report{Rooms: 2, RoomsCreated: 1, MemberCounts: 2, RoomInfos: 1, Messages: 61, Members: 2, Preferences: 1}); rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	want := slices.Concat(
		[]store.StateTarget{roomMark(busyRoom, store.BitMemberCount, 1)},
		timelineMarks(busyRoom, 91, 31),
		[]store.StateTarget{
			memberMark(busyRoom, "alice", store.BitMember, 1), memberMark(busyRoom, "alice", store.BitPreferences, 1),
			roomMark(newRoom, store.BitRoomCreated, 0), roomMark(newRoom, store.BitMemberCount, 1), roomMark(newRoom, store.BitRoomInfo, 2),
			memberMark(newRoom, "alice", store.BitMember, 1),
		},
	)
	assertMarked(t, spy.marked(), want)
	if rep.String() != "resync rooms=2 marked rooms.created=1 rooms.member_count=2 rooms.info=1 messages=61 message_edits=0 message_interactions.reaction=0 message_interactions.bookmark=0 pin_actions=0 members=2 members.read=0 members.cleared=0 members.preferences=1 hidden=0 count_checks=0 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}
