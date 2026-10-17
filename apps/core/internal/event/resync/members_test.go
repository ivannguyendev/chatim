package resync_test

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func memberMark(room uint64, user string, bit store.StateBit, ver uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateMembers, Bit: bit, Room: room, User: user, Ver: ver}
}

func (w world) join(t *testing.T, room uint64, at time.Time, users ...string) {
	t.Helper()
	j := domain.Join{Room: room, Tenant: "acme", RequestID: "join-" + strconv.FormatInt(at.UnixNano(), 10), By: "alice", At: at}
	if _, err := w.rooms.AddMembers(t.Context(), j, users); err != nil {
		t.Fatalf("AddMembers(%d %v): %v", room, users, err)
	}
}

func (w world) hide(t *testing.T, room, seq uint64, user string, at time.Time) store.StateTarget {
	t.Helper()
	if hidden, err := w.hidden.Hide(t.Context(), user, store.MsgKey{Room: room, Seq: seq}, at); err != nil || !hidden {
		t.Fatalf("Hide(%d/%d %s) = %v, %v; want newly hidden", room, seq, user, hidden, err)
	}
	return store.StateTarget{Collection: store.StateHidden, Bit: store.BitHidden, Room: room, Seq: seq, User: user}
}

func TestResyncMarksTheCurrentMemberDocsOfTheLostRangeWithTheirOptionalBits(t *testing.T) {
	w := newWorld(t)
	w.join(t, newRoom, lostFrom.Add(20*time.Minute), "bob", "carol")
	clearedAt, raised, err := w.rooms.ClearHistory(t.Context(), newRoom, "carol", lostFrom.Add(30*time.Minute))
	if err != nil || !raised {
		t.Fatalf("ClearHistory = %v, %v; want raised", raised, err)
	}
	w.join(t, newRoom, lostTo.Add(time.Minute), "dave")
	w.join(t, busyRoom, lostFrom.Add(-time.Minute), "erin")
	pinned := w.pin(t, newRoom, 1, 1, lostFrom.Add(40*time.Minute))
	spy := w.spy()
	rep, err := resync.Run(t.Context(), w.deps(spy), resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := resync.Report{Rooms: 2, RoomsCreated: 1, MemberCounts: 2, Messages: 61, Pins: 1, Members: 3, ReadPositions: 2, HistoryCleared: 1}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	cleared := memberMark(newRoom, "carol", store.BitHistoryCleared, 0)
	cleared.At = clearedAt
	marks := slices.Concat(
		[]store.StateTarget{roomMark(busyRoom, store.BitMemberCount, 1)},
		timelineMarks(busyRoom, 91, 31),
		[]store.StateTarget{
			roomMark(newRoom, store.BitRoomCreated, 0), roomMark(newRoom, store.BitMemberCount, 1), pinned,
			memberMark(newRoom, "alice", store.BitMember, 1),
			memberMark(newRoom, "bob", store.BitMember, 1), memberMark(newRoom, "bob", store.BitReadPosition, 1),
			memberMark(newRoom, "carol", store.BitMember, 1), memberMark(newRoom, "carol", store.BitReadPosition, 1), cleared,
		},
	)
	assertMarked(t, spy.marked(), marks)
	if rep.String() != "resync rooms=2 marked rooms.created=1 rooms.member_count=2 rooms.info=0 messages=61 message_edits=0 message_interactions.reaction=0 message_interactions.bookmark=0 pin_actions=1 members=3 members.read=2 members.cleared=1 members.preferences=0 hidden=0 count_checks=0 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}

func TestResyncMarksHiddenMessagesOfTheLostRange(t *testing.T) {
	w := newWorld(t)
	hidden := w.hide(t, busyRoom, 40, "bob", lostFrom.Add(5*time.Minute))
	w.hide(t, busyRoom, 41, "carol", lostFrom.Add(-time.Minute))
	w.hide(t, busyRoom, 42, "dave", lostTo.Add(time.Minute))
	spy := w.spy()
	rep, err := resync.Run(t.Context(), w.deps(spy), resync.Options{From: lostFrom, To: lostTo, Room: busyRoom, Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := (resync.Report{Rooms: 1, MemberCounts: 1, Messages: 61, Hidden: 1}); rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	marks := slices.Concat([]store.StateTarget{roomMark(busyRoom, store.BitMemberCount, 1)}, timelineMarks(busyRoom, 91, 31), []store.StateTarget{hidden})
	assertMarked(t, spy.marked(), marks)
}

func TestResyncPagesMemberDocsByTimeWithoutRepeatingThePageEdge(t *testing.T) {
	w := newWorld(t)
	for i := range 400 {
		at := lostFrom.Add(time.Duration(i+1) * time.Millisecond)
		users := make([]string, 3)
		for j := range users {
			users[j] = "u" + strconv.Itoa(3*i+j+1)
			w.hide(t, staleRoom, uint64(j+1), users[j], at)
		}
		w.join(t, staleRoom, at, users...)
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(w.spy()), opts)
	if err != nil || rep != (resync.Report{Rooms: 1, MemberCounts: 1, Members: 1200, ReadPositions: 1200, Hidden: 1200, DryRun: true}) {
		t.Fatalf("Run = %+v, %v; want 1200 member docs with their read positions and 1200 hidden messages, each once", rep, err)
	}
}

func TestResyncStopsWhenOneInstantHoldsMoreMemberChangesThanAPage(t *testing.T) {
	at := lostFrom.Add(time.Minute)
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	users := make([]string, 1001)
	for i := range users {
		users[i] = "u" + strconv.Itoa(i+1)
	}
	w := newWorld(t)
	w.join(t, staleRoom, at, users[:1000]...)
	w.join(t, staleRoom, at, users[1000:]...)
	rep, err := resync.Run(t.Context(), w.deps(w.spy()), opts)
	if !errors.Is(err, resync.ErrMemberPageFull) || rep.Members != 1000 || rep.ReadPositions != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrMemberPageFull after one full page", rep, err)
	}
	w = newWorld(t)
	for i, u := range users {
		w.hide(t, staleRoom, uint64(i+1), u, at)
	}
	rep, err = resync.Run(t.Context(), w.deps(w.spy()), opts)
	if !errors.Is(err, resync.ErrHiddenPageFull) || rep.Hidden != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrHiddenPageFull after one full page", rep, err)
	}
}
