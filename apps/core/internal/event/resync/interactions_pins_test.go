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

func (w world) react(t *testing.T, room, seq uint64, user, emoji string, at time.Time) string {
	t.Helper()
	r, changed, err := w.reactions.SetReaction(t.Context(), domain.Reaction{Room: room, Seq: seq, Tenant: "acme", User: user, Emoji: emoji, At: at})
	if err != nil || !changed {
		t.Fatalf("Set(%d/%d %s %q) = %+v, %v, %v; want a change", room, seq, user, emoji, r, changed, err)
	}
	return work.Record{Kind: store.ReactionChanged, Room: room, Seq: seq, Version: r.N, User: user}.ID()
}

func (w world) unreact(t *testing.T, room, seq uint64, user string, at time.Time) string {
	t.Helper()
	r, changed, err := w.reactions.RemoveReaction(t.Context(), store.MsgKey{Room: room, Seq: seq}, user, at)
	if err != nil || !changed {
		t.Fatalf("Remove(%d/%d %s) = %+v, %v, %v; want a change", room, seq, user, r, changed, err)
	}
	return work.Record{Kind: store.ReactionChanged, Room: room, Seq: seq, Version: r.N, User: user}.ID()
}

func (w world) pin(t *testing.T, room, pv, seq uint64, at time.Time) string {
	t.Helper()
	op := domain.PinOpPin
	if pv%2 == 0 {
		op = domain.PinOpUnpin
	}
	a := domain.PinAction{Room: room, PV: pv, Tenant: "acme", Op: op, Seq: seq, By: "alice", At: at}
	if err := w.pins.Append(t.Context(), a); err != nil {
		t.Fatalf("Append(%d p%d): %v", room, pv, err)
	}
	return work.Record{Kind: store.PinInserted, Room: room, Seq: pv}.ID()
}

func TestResyncPublishesReactionsAndPinsOfTheLostRangeAfterTheEdits(t *testing.T) {
	w := newWorld(t)
	edit := w.edit(t, busyRoom, 40, 1, lostFrom.Add(5*time.Minute))
	w.react(t, busyRoom, 40, "bob", "👍", lostFrom.Add(5*time.Minute))
	changed := w.react(t, busyRoom, 40, "bob", "🎉", lostFrom.Add(6*time.Minute))
	w.react(t, busyRoom, 41, "carol", "👍", lostFrom.Add(-time.Minute))
	w.react(t, busyRoom, 42, "dave", "👍", lostFrom.Add(7*time.Minute))
	removed := w.unreact(t, busyRoom, 42, "dave", lostFrom.Add(8*time.Minute))
	pinned := w.pin(t, busyRoom, 1, 40, lostFrom.Add(9*time.Minute))
	w.pin(t, busyRoom, 2, 40, lostTo.Add(time.Minute))
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, EditRecords: 1, ReactionRecords: 2, PinRecords: 1, MemberRecords: 1, CountCheckRecords: 2}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	got := withoutOps(pub.published())
	tail := []string{
		edit, changed, removed, pinned, countCheckID(busyRoom, 40, "reactions"), countCheckID(busyRoom, 42, "reactions"),
		work.Record{Kind: store.RoomInserted, Room: newRoom}.ID(), memberID(newRoom, "alice", 1),
	}
	if len(got) != 69 || !slices.Equal(got[61:], tail) {
		t.Fatalf("published %d ids ending %v, want 61 messages then %v", len(got), got[max(0, len(got)-8):], tail)
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=1 reaction_records=2 bookmark_records=0 pin_records=1 member_records=1 hidden_records=0 count_check_records=2 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}

func TestResyncPagesReactionsByCursorAndPinsByTimeWithoutRepeatingThePageEdge(t *testing.T) {
	w := newWorld(t)
	for i := range uint32(1200) {
		at := lostFrom.Add(time.Duration((i+1)/3) * time.Millisecond)
		w.react(t, staleRoom, 1, "u"+strconv.FormatUint(uint64(i+1), 10), "👍", at)
		w.pin(t, staleRoom, uint64(i+1), 1, at)
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if err != nil || rep != (resync.Report{Rooms: 1, ReactionRecords: 1200, PinRecords: 1200, CountCheckRecords: 1, DryRun: true}) {
		t.Fatalf("Run = %+v, %v; want 1200 reaction and 1200 pin records, each once, and one count check", rep, err)
	}
}

func TestResyncPagesOneInstantOfReactionsButStopsOnAFullPageOfPins(t *testing.T) {
	at := lostFrom.Add(time.Minute)
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	w := newWorld(t)
	for i := range 1001 {
		w.react(t, staleRoom, 1, "u"+strconv.Itoa(i+1), "👍", at)
	}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if err != nil || rep.ReactionRecords != 1001 || rep.CountCheckRecords != 1 {
		t.Fatalf("Run = %+v, %v; want all 1001 reactions of one instant and one count check", rep, err)
	}
	w = newWorld(t)
	for pv := range uint64(1001) {
		w.pin(t, staleRoom, pv+1, 1, at)
	}
	rep, err = resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if !errors.Is(err, resync.ErrPinPageFull) || rep.PinRecords != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrPinPageFull after one full page", rep, err)
	}
}
