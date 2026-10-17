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
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (w world) react(t *testing.T, room, seq uint64, user, emoji string, at time.Time) store.StateTarget {
	t.Helper()
	r, changed, err := w.reactions.SetReaction(t.Context(), domain.Reaction{Room: room, Seq: seq, Tenant: "acme", User: user, Emoji: emoji, At: at})
	if err != nil || !changed {
		t.Fatalf("Set(%d/%d %s %q) = %+v, %v, %v; want a change", room, seq, user, emoji, r, changed, err)
	}
	return interactionMark(room, seq, keys.ReactionKind, user, r.N)
}

func (w world) unreact(t *testing.T, room, seq uint64, user string, at time.Time) store.StateTarget {
	t.Helper()
	r, changed, err := w.reactions.RemoveReaction(t.Context(), store.MsgKey{Room: room, Seq: seq}, user, at)
	if err != nil || !changed {
		t.Fatalf("Remove(%d/%d %s) = %+v, %v, %v; want a change", room, seq, user, r, changed, err)
	}
	return interactionMark(room, seq, keys.ReactionKind, user, r.N)
}

func (w world) pin(t *testing.T, room, pv, seq uint64, at time.Time) store.StateTarget {
	t.Helper()
	op := domain.PinOpPin
	if pv%2 == 0 {
		op = domain.PinOpUnpin
	}
	a := domain.PinAction{Room: room, PV: pv, Tenant: "acme", Op: op, Seq: seq, By: "alice", At: at}
	if err := w.pins.Append(t.Context(), a); err != nil {
		t.Fatalf("Append(%d p%d): %v", room, pv, err)
	}
	return store.StateTarget{Collection: store.StatePins, Bit: store.BitPinFact, Room: room, Seq: pv}
}

func TestResyncMarksReactionsAndPinsOfTheLostRangeAfterTheEdits(t *testing.T) {
	w := newWorld(t)
	edit := w.edit(t, busyRoom, 40, 1, lostFrom.Add(5*time.Minute))
	w.react(t, busyRoom, 40, "bob", "👍", lostFrom.Add(5*time.Minute))
	changed := w.react(t, busyRoom, 40, "bob", "🎉", lostFrom.Add(6*time.Minute))
	w.react(t, busyRoom, 41, "carol", "👍", lostFrom.Add(-time.Minute))
	w.react(t, busyRoom, 42, "dave", "👍", lostFrom.Add(7*time.Minute))
	removed := w.unreact(t, busyRoom, 42, "dave", lostFrom.Add(8*time.Minute))
	pinned := w.pin(t, busyRoom, 1, 40, lostFrom.Add(9*time.Minute))
	w.pin(t, busyRoom, 2, 40, lostTo.Add(time.Minute))
	spy := w.spy()
	rep, err := resync.Run(t.Context(), w.deps(spy), resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := resync.Report{Rooms: 2, RoomsCreated: 1, MemberCounts: 2, Messages: 61, Edits: 1, Reactions: 2, Pins: 1, Members: 1, CountChecks: 2}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	marks := slices.Concat(
		[]store.StateTarget{roomMark(busyRoom, store.BitMemberCount, 1)},
		timelineMarks(busyRoom, 91, 31),
		[]store.StateTarget{
			edit, changed, removed, pinned,
			roomMark(newRoom, store.BitRoomCreated, 0), roomMark(newRoom, store.BitMemberCount, 1), memberMark(newRoom, "alice", store.BitMember, 1),
		},
	)
	assertMarked(t, spy.marked(), marks)
	if checks := []string{countCheckID(busyRoom, 40, "reactions"), countCheckID(busyRoom, 42, "reactions")}; !slices.Equal(spy.checks, checks) {
		t.Fatalf("count checks = %v, want %v", spy.checks, checks)
	}
	if rep.String() != "resync rooms=2 marked rooms.created=1 rooms.member_count=2 rooms.info=0 messages=61 message_edits=1 message_interactions.reaction=2 message_interactions.bookmark=0 pin_actions=1 members=1 members.read=0 members.cleared=0 members.preferences=0 hidden=0 count_checks=2 dry_run=false" {
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
	rep, err := resync.Run(t.Context(), w.deps(w.spy()), opts)
	if err != nil || rep != (resync.Report{Rooms: 1, MemberCounts: 1, Reactions: 1200, Pins: 1200, CountChecks: 1, DryRun: true}) {
		t.Fatalf("Run = %+v, %v; want 1200 reactions and 1200 pins, each once, and one count check", rep, err)
	}
}

func TestResyncPagesOneInstantOfReactionsButStopsOnAFullPageOfPins(t *testing.T) {
	at := lostFrom.Add(time.Minute)
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	w := newWorld(t)
	for i := range 1001 {
		w.react(t, staleRoom, 1, "u"+strconv.Itoa(i+1), "👍", at)
	}
	rep, err := resync.Run(t.Context(), w.deps(w.spy()), opts)
	if err != nil || rep.Reactions != 1001 || rep.CountChecks != 1 {
		t.Fatalf("Run = %+v, %v; want all 1001 reactions of one instant and one count check", rep, err)
	}
	w = newWorld(t)
	for pv := range uint64(1001) {
		w.pin(t, staleRoom, pv+1, 1, at)
	}
	rep, err = resync.Run(t.Context(), w.deps(w.spy()), opts)
	if !errors.Is(err, resync.ErrPinPageFull) || rep.Pins != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrPinPageFull after one full page", rep, err)
	}
}
