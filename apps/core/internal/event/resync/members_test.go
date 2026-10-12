package resync_test

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (w world) join(t *testing.T, room uint64, at time.Time, users ...string) {
	t.Helper()
	j := domain.Join{Room: room, Tenant: "acme", RequestID: "join-" + strconv.FormatInt(at.UnixNano(), 10), By: "alice", At: at}
	if _, err := w.rooms.AddMembers(t.Context(), j, users); err != nil {
		t.Fatalf("AddMembers(%d %v): %v", room, users, err)
	}
}

func (w world) hide(t *testing.T, room, seq uint64, user string, at time.Time) string {
	t.Helper()
	if hidden, err := w.hidden.Hide(t.Context(), user, store.MsgKey{Room: room, Seq: seq}, at); err != nil || !hidden {
		t.Fatalf("Hide(%d/%d %s) = %v, %v; want newly hidden", room, seq, user, hidden, err)
	}
	return work.Record{Kind: store.MessageHidden, Room: room, Seq: seq, User: user}.ID()
}

func memberID(room uint64, user string, ver uint32) string {
	return work.Record{Kind: store.MemberChanged, Room: room, User: user, Version: ver}.ID()
}

func readID(room uint64, user string, ver uint32) string {
	return work.Record{Kind: store.ReadChanged, Room: room, User: user, Version: ver}.ID()
}

func TestResyncPublishesTheCurrentMemberDocsOfTheLostRangeLast(t *testing.T) {
	w := newWorld(t)
	w.join(t, newRoom, lostFrom.Add(20*time.Minute), "bob", "carol")
	clearedAt := lostFrom.Add(30 * time.Minute)
	if _, raised, err := w.rooms.ClearHistory(t.Context(), newRoom, "carol", clearedAt); err != nil || !raised {
		t.Fatalf("ClearHistory = %v, %v; want raised", raised, err)
	}
	w.join(t, newRoom, lostTo.Add(time.Minute), "dave")
	w.join(t, busyRoom, lostFrom.Add(-time.Minute), "erin")
	pinned := w.pin(t, newRoom, 1, 1, lostFrom.Add(40*time.Minute))
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, PinRecords: 1, MemberRecords: 6}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	cleared := work.Record{Kind: store.HistoryCleared, Room: newRoom, User: "carol", CommittedAt: clearedAt}.ID()
	tail := []string{
		work.Record{Kind: store.RoomInserted, Room: newRoom}.ID(), pinned,
		memberID(newRoom, "alice", 1),
		memberID(newRoom, "bob", 1), readID(newRoom, "bob", 1),
		memberID(newRoom, "carol", 1), readID(newRoom, "carol", 1), cleared,
	}
	if got := pub.published(); len(got) != 69 || !slices.Equal(got[61:], tail) {
		t.Fatalf("published %d ids ending %v, want 61 messages then %v", len(got), got[max(0, len(got)-8):], tail)
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=0 reaction_records=0 bookmark_records=0 pin_records=1 member_records=6 hidden_records=0 count_check_records=0 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}

func TestResyncReplaysHiddenMessagesOfTheLostRange(t *testing.T) {
	w := newWorld(t)
	hidden := w.hide(t, busyRoom, 40, "bob", lostFrom.Add(5*time.Minute))
	w.hide(t, busyRoom, 41, "carol", lostFrom.Add(-time.Minute))
	w.hide(t, busyRoom, 42, "dave", lostTo.Add(time.Minute))
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, MemberRecords: 1, HiddenRecords: 1}); rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	tail := []string{hidden, work.Record{Kind: store.RoomInserted, Room: newRoom}.ID(), memberID(newRoom, "alice", 1)}
	if got := pub.published(); len(got) != 64 || !slices.Equal(got[61:], tail) {
		t.Fatalf("published %d ids ending %v, want 61 messages then %v", len(got), got[max(0, len(got)-3):], tail)
	}
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
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if err != nil || rep != (resync.Report{Rooms: 1, MemberRecords: 2400, HiddenRecords: 1200, DryRun: true}) {
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
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if !errors.Is(err, resync.ErrMemberPageFull) || rep.MemberRecords != 2000 {
		t.Fatalf("Run = %+v, %v; want ErrMemberPageFull after one full page", rep, err)
	}
	w = newWorld(t)
	for i, u := range users {
		w.hide(t, staleRoom, uint64(i+1), u, at)
	}
	rep, err = resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if !errors.Is(err, resync.ErrHiddenPageFull) || rep.HiddenRecords != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrHiddenPageFull after one full page", rep, err)
	}
}
