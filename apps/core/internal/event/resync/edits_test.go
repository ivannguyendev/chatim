package resync_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (w world) edit(t *testing.T, room, seq uint64, version uint32, at time.Time) string {
	t.Helper()
	e := domain.Edit{Room: room, Seq: seq, Version: version, Kind: domain.EditText, Tenant: "acme", By: "alice", Text: "edited", At: at}
	if err := w.edits.Append(t.Context(), e); err != nil {
		t.Fatalf("Append(%d/%d v%d): %v", room, seq, version, err)
	}
	return work.Record{Kind: store.EditInserted, Room: room, Seq: seq, Version: version}.ID()
}

func TestResyncPublishesEditsOfTheLostRangeAfterTheTimeline(t *testing.T) {
	w := newWorld(t)
	inRange := w.edit(t, busyRoom, 40, 1, lostFrom.Add(5*time.Minute))
	w.edit(t, busyRoom, 40, 2, lostTo.Add(time.Minute))
	w.edit(t, busyRoom, 3, 1, lostFrom.Add(-time.Minute))
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, EditRecords: 1, MemberRecords: 1}); rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	got := pub.published()
	roomRecord := work.Record{Kind: store.RoomInserted, Room: newRoom}.ID()
	if len(got) != 64 || got[61] != inRange || got[62] != roomRecord {
		t.Fatalf("published %d ids ending %v, want 61 messages, then %s, then %s", len(got), got[max(0, len(got)-3):], inRange, roomRecord)
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=1 reaction_records=0 pin_records=0 member_records=1 hidden_records=0 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}

func TestResyncPagesEditsByTimeWithoutRepeatingThePageEdge(t *testing.T) {
	w := newWorld(t)
	for v := range uint32(1200) {
		w.edit(t, staleRoom, 1, v+1, lostFrom.Add(time.Duration((v+1)/3)*time.Millisecond))
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if err != nil || rep != (resync.Report{Rooms: 1, EditRecords: 1200, DryRun: true}) {
		t.Fatalf("Run = %+v, %v; want 1200 edit records, each once", rep, err)
	}
}

func TestResyncStopsWhenOneInstantHoldsMoreEditsThanAPage(t *testing.T) {
	w := newWorld(t)
	at := lostFrom.Add(time.Minute)
	for v := range uint32(1001) {
		w.edit(t, staleRoom, 1, v+1, at)
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if !errors.Is(err, resync.ErrEditPageFull) || rep.EditRecords != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrEditPageFull after one full page", rep, err)
	}
}
