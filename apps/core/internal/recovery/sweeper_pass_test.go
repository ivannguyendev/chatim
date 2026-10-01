package recovery_test

import (
	"cmp"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/recovery"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

type passWorld struct {
	mr    *miniredis.Miniredis
	msgs  *memstore.Messages
	rooms *recorder
	clk   *clock
	sw    *recovery.Sweeper
}

func newPassWorld(t *testing.T, cfg recovery.Config, beforeLast func(room uint64) error) *passWorld {
	t.Helper()
	mr, rdb := newRedis(t)
	pw := &passWorld{mr: mr, msgs: memstore.NewMessages(), rooms: newRecorder(nil), clk: fixedClock()}
	var timeline recovery.Timeline = pw.msgs
	if beforeLast != nil {
		timeline = hookedTimeline{Timeline: pw.msgs, before: beforeLast}
	}
	pw.sw = newSweeper(t, recovery.Deps{Slots: owning(slotA), Rooms: pw.rooms, Msgs: timeline, Redis: rdb}, cfg, pw.clk)
	return pw
}

func (pw *passWorld) pass(t *testing.T) { pw.sw.Pass(t.Context(), []uint16{slotA}) }

func (pw *passWorld) recovered() []call {
	got := pw.rooms.list()
	slices.SortStableFunc(got, func(a, b call) int { return cmp.Compare(a.room, b.room) })
	return got
}

func TestCaughtUpRoomIsRemovedOnlyOnceItsMarkIsOlderThanRemoveAfter(t *testing.T) {
	pw := newPassWorld(t, passSetup, nil)
	seed(t, pw.msgs, roomA, time.Now(), 1, 2, 3)
	setWatermark(t, pw.mr, roomA, "3")
	mark(t, pw.mr, roomA, pw.clk.now())

	pw.clk.advance(recovery.DefaultRemoveAfter - time.Millisecond)
	pw.pass(t)
	if !isActive(pw.mr, roomA) {
		t.Fatal("caught-up room removed while its mark was younger than RemoveAfter")
	}
	pw.clk.advance(time.Millisecond)
	pw.pass(t)
	if isActive(pw.mr, roomA) {
		t.Fatal("caught-up room with an old mark kept")
	}
	if calls := pw.recovered(); len(calls) != 0 {
		t.Fatalf("recovered a caught-up room: %v", calls)
	}
}

func TestBehindRoomIsRecoveredFromItsWatermarkAndNeverRemoved(t *testing.T) {
	pw := newPassWorld(t, passSetup, nil)
	seed(t, pw.msgs, roomA, time.Now(), 1, 2, 3, 4, 5)
	setWatermark(t, pw.mr, roomA, "2")
	mark(t, pw.mr, roomA, pw.clk.now().Add(-time.Hour))

	pw.pass(t)
	pw.pass(t)
	if got, want := pw.recovered(), []call{{roomA, 2}, {roomA, 2}}; !slices.Equal(got, want) {
		t.Fatalf("recover calls = %v, want %v", got, want)
	}
	if !isActive(pw.mr, roomA) {
		t.Fatal("room still behind its timeline was removed")
	}
}

func TestMissingOrMalformedWatermarksRecoverFromTheStart(t *testing.T) {
	pw := newPassWorld(t, passSetup, nil)
	ids := roomsInSlot(slotA, 4)
	for i, room := range ids {
		seed(t, pw.msgs, room, time.Now(), 1, 2)
		mark(t, pw.mr, room, time.Now().Add(time.Duration(i)*time.Millisecond))
	}
	setWatermark(t, pw.mr, ids[1], "abc")
	setWatermark(t, pw.mr, ids[2], "18446744073709551616")
	setWatermark(t, pw.mr, ids[3], "1")

	pw.pass(t)
	want := []call{{ids[0], 0}, {ids[1], 0}, {ids[2], 0}, {ids[3], 1}}
	if got := pw.recovered(); !slices.Equal(got, want) {
		t.Fatalf("recover calls = %v, want %v", got, want)
	}
}

func TestRoomRemarkedAfterTheReadIsKept(t *testing.T) {
	var pw *passWorld
	pw = newPassWorld(t, passSetup, func(room uint64) error {
		_, err := pw.mr.ZAdd(publish.ActiveKey(slotA), float64(time.Now().UnixMilli()), pbconv.RoomID(room))
		return err
	})
	seed(t, pw.msgs, roomA, time.Now(), 1)
	setWatermark(t, pw.mr, roomA, "1")
	old := pw.clk.now().Add(-time.Hour)
	mark(t, pw.mr, roomA, old)

	pw.pass(t)
	if !isActive(pw.mr, roomA) {
		t.Fatal("room re-marked during the pass was removed")
	}
	if score, _ := pw.mr.ZScore(publish.ActiveKey(slotA), pbconv.RoomID(roomA)); score == float64(old.UnixMilli()) {
		t.Fatal("the re-mark did not land before the removal check")
	}
}

func TestFailedChecksKeepTheirRoomsWhileOthersProceed(t *testing.T) {
	ids := roomsInSlot(slotA, 3)
	errStore := errors.New("store down")
	pw := newPassWorld(t, passSetup, func(room uint64) error {
		if room == ids[0] {
			return errStore
		}
		return nil
	})
	old := pw.clk.now().Add(-time.Hour)
	for _, room := range ids {
		seed(t, pw.msgs, room, old, 1, 2)
		mark(t, pw.mr, room, old)
	}
	setWatermark(t, pw.mr, ids[0], "2")
	setWatermark(t, pw.mr, ids[2], "2")
	pw.rooms.failFor(ids[1], errors.New("mailbox full"))
	if _, err := pw.mr.ZAdd(publish.ActiveKey(slotA), float64(old.UnixMilli()), "not-a-room"); err != nil {
		t.Fatalf("ZAdd: %v", err)
	}

	pw.pass(t)
	if !isActive(pw.mr, ids[0]) || !isActive(pw.mr, ids[1]) {
		t.Fatal("room whose check failed was removed")
	}
	if isActive(pw.mr, ids[2]) {
		t.Fatal("caught-up room next to failing ones was kept")
	}
	if members, _ := pw.mr.ZMembers(publish.ActiveKey(slotA)); !slices.Contains(members, "not-a-room") {
		t.Fatal("malformed member was touched")
	}
}

func TestEmptyRoomWithoutWatermarkIsRemovedOnceOld(t *testing.T) {
	pw := newPassWorld(t, passSetup, nil)
	mark(t, pw.mr, roomA, pw.clk.now().Add(-time.Hour))
	pw.pass(t)
	if isActive(pw.mr, roomA) {
		t.Fatal("room with nothing to publish kept")
	}
}

func TestLargeSlotIsSweptInBatchesAcrossPasses(t *testing.T) {
	cfg := passSetup
	cfg.Batch, cfg.Workers = 2, 1
	pw := newPassWorld(t, cfg, nil)
	ids := roomsInSlot(slotA, 5)
	old := pw.clk.now().Add(-time.Hour)
	for i, room := range ids {
		seed(t, pw.msgs, room, old, 1)
		mark(t, pw.mr, room, old.Add(time.Duration(i)*time.Millisecond))
	}
	setWatermark(t, pw.mr, ids[0], "1")

	for range 4 {
		pw.pass(t)
	}
	var got []uint64
	for _, c := range pw.rooms.list() {
		got = append(got, c.room)
	}
	if want := []uint64{ids[1], ids[2], ids[3], ids[4], ids[1], ids[2]}; !slices.Equal(got, want) {
		t.Fatalf("recovered rooms in order %v, want %v", got, want)
	}
	if isActive(pw.mr, ids[0]) {
		t.Fatal("caught-up room of the first batch kept")
	}
}
