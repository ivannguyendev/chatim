package recovery_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/recovery"
)

func TestBusyRoomSlightlyBehindIsLeftToThePublisherUntilItGoesStale(t *testing.T) {
	pw := newPassWorld(t, passSetup, nil)
	seed(t, pw.msgs, roomA, pw.clk.now().Add(-time.Minute), 1, 2, 3)
	seed(t, pw.msgs, roomA, pw.clk.now(), 4)
	setWatermark(t, pw.mr, roomA, "3")
	mark(t, pw.mr, roomA, pw.clk.now().Add(-time.Hour))

	pw.pass(t)
	pw.clk.advance(recovery.DefaultStaleAfter - time.Millisecond)
	pw.pass(t)
	if calls := pw.recovered(); len(calls) != 0 {
		t.Fatalf("recovered a room whose next message is still in flight: %v", calls)
	}
	if !isActive(pw.mr, roomA) {
		t.Fatal("room behind its timeline removed while deferred")
	}

	pw.clk.advance(time.Millisecond)
	pw.pass(t)
	if got, want := pw.recovered(), []call{{roomA, 3}}; !slices.Equal(got, want) {
		t.Fatalf("recover calls once stale = %v, want %v", got, want)
	}
	pw.pass(t)
	if got, want := pw.recovered(), []call{{roomA, 3}, {roomA, 3}}; !slices.Equal(got, want) {
		t.Fatalf("recover calls for a room that stays stuck = %v, want %v", got, want)
	}
	if !isActive(pw.mr, roomA) {
		t.Fatal("stuck room removed")
	}
}

func TestMissingWatermarkWaitsForTheFirstMessageToGoStale(t *testing.T) {
	pw := newPassWorld(t, passSetup, nil)
	seed(t, pw.msgs, roomA, pw.clk.now(), 1, 2)
	mark(t, pw.mr, roomA, pw.clk.now())

	pw.pass(t)
	if calls := pw.recovered(); len(calls) != 0 {
		t.Fatalf("recovered a new room whose first message is still in flight: %v", calls)
	}
	pw.clk.advance(recovery.DefaultStaleAfter)
	pw.pass(t)
	if got, want := pw.recovered(), []call{{roomA, 0}}; !slices.Equal(got, want) {
		t.Fatalf("recover calls once stale = %v, want %v", got, want)
	}
}

func TestStaleAfterIsMeasuredFromTheMessageRightAfterTheWatermark(t *testing.T) {
	pw := newPassWorld(t, passSetup, nil)
	seed(t, pw.msgs, roomA, pw.clk.now().Add(-time.Minute), 1, 2)
	seed(t, pw.msgs, roomA, pw.clk.now(), 3)
	setWatermark(t, pw.mr, roomA, "1")
	mark(t, pw.mr, roomA, pw.clk.now())

	pw.pass(t)
	if got, want := pw.recovered(), []call{{roomA, 1}}; !slices.Equal(got, want) {
		t.Fatalf("recover calls = %v, want %v: pts 2 is old even though the newest message is fresh", got, want)
	}
}
