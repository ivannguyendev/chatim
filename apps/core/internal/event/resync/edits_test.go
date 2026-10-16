package resync_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

func (w world) edit(t *testing.T, room, seq uint64, version uint32, at time.Time) store.StateTarget {
	t.Helper()
	e := domain.Edit{Room: room, Seq: seq, Version: version, Kind: domain.EditText, Tenant: "acme", By: "alice", Text: "edited", At: at}
	if err := w.edits.Append(t.Context(), e); err != nil {
		t.Fatalf("Append(%d/%d v%d): %v", room, seq, version, err)
	}
	return store.StateTarget{Collection: store.StateEdits, Bit: store.BitEditFact, Room: room, Seq: seq, Ver: uint64(version)}
}

type withOriginals struct{ *memstore.Edits }

func (e withOriginals) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error) {
	page, err := e.Edits.Between(ctx, room, from, to, limit)
	var out []domain.Edit
	for _, f := range page {
		original := f
		original.Version, original.Kind = 0, domain.EditOriginal
		out = append(out, original, f)
	}
	return out, err
}

func TestResyncMarksEditFactsOfTheLostRangeButNotTheOriginalRows(t *testing.T) {
	w := newWorld(t)
	inRange := w.edit(t, busyRoom, 40, 1, lostFrom.Add(5*time.Minute))
	w.edit(t, busyRoom, 40, 2, lostTo.Add(time.Minute))
	w.edit(t, busyRoom, 3, 1, lostFrom.Add(-time.Minute))
	spy := w.spy()
	deps := w.deps(spy)
	deps.Edits = withOriginals{w.edits}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: busyRoom, Rate: resync.MaxRate}
	rep, err := resync.Run(t.Context(), deps, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := (resync.Report{Rooms: 1, MemberCounts: 1, Messages: 61, Edits: 1}); rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	want := slices.Concat([]store.StateTarget{roomMark(busyRoom, store.BitMemberCount, 1)}, timelineMarks(busyRoom, 91, 31), []store.StateTarget{inRange})
	assertMarked(t, spy.marked(), want)
}

func TestResyncPagesEditsByTimeWithoutRepeatingThePageEdge(t *testing.T) {
	w := newWorld(t)
	for v := range uint32(1200) {
		w.edit(t, staleRoom, 1, v+1, lostFrom.Add(time.Duration((v+1)/3)*time.Millisecond))
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(w.spy()), opts)
	if err != nil || rep != (resync.Report{Rooms: 1, MemberCounts: 1, Edits: 1200, DryRun: true}) {
		t.Fatalf("Run = %+v, %v; want 1200 edit facts, each once", rep, err)
	}
}

func TestResyncStopsWhenOneInstantHoldsMoreEditsThanAPage(t *testing.T) {
	w := newWorld(t)
	at := lostFrom.Add(time.Minute)
	for v := range uint32(1001) {
		w.edit(t, staleRoom, 1, v+1, at)
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(w.spy()), opts)
	if !errors.Is(err, resync.ErrEditPageFull) || rep.Edits != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrEditPageFull after one full page", rep, err)
	}
}
