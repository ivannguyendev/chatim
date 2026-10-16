package resync_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestResyncMarksDocsOfTheLostRangeOnly(t *testing.T) {
	w := newWorld(t)
	spy := w.spy()
	rep, err := resync.Run(t.Context(), w.deps(spy), resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := slices.Concat(
		[]store.StateTarget{roomMark(busyRoom, store.BitMemberCount, 1)},
		timelineMarks(busyRoom, 91, 31),
		[]store.StateTarget{roomMark(newRoom, store.BitRoomCreated, 0), roomMark(newRoom, store.BitMemberCount, 1), memberMark(newRoom, "alice", store.BitMember, 1)},
	)
	assertMarked(t, spy.marked(), want)
	if sizes := pageSizes(spy); !slices.Equal(sizes, []int{1, 61, 2, 1}) {
		t.Fatalf("Mark calls of %v targets, want one call per collection run: [1 61 2 1]", sizes)
	}
	if rep != (resync.Report{Rooms: 2, RoomsCreated: 1, MemberCounts: 2, Messages: 61, Members: 1}) {
		t.Fatalf("report = %+v, want 2 rooms, 1 created, 2 member counts, 61 messages, 1 member", rep)
	}
}

func TestResyncMarksInPagesOfOneCollection(t *testing.T) {
	w := newWorld(t)
	w.messages(t, newRoom, 450, lostFrom.Add(time.Minute))
	spy := w.spy()
	opts := resync.Options{From: lostFrom, To: lostFrom.Add(10 * time.Hour), Room: newRoom, Rate: resync.MaxRate}
	if _, err := resync.Run(t.Context(), w.deps(spy), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sizes := pageSizes(spy); !slices.Equal(sizes, []int{2, 200, 200, 50, 1}) {
		t.Fatalf("Mark calls of %v targets, want [2 200 200 50 1]", sizes)
	}
}

func TestResyncOfOneRoomSkipsTheActivityIndex(t *testing.T) {
	w := newWorld(t)
	spy := w.spy()
	opts := resync.Options{From: lostFrom.Add(-4 * time.Hour), To: lostFrom.Add(-2 * time.Hour), Room: staleRoom, Rate: resync.MaxRate}
	rep, err := resync.Run(t.Context(), w.deps(spy), opts)
	if err != nil || rep != (resync.Report{Rooms: 1, MemberCounts: 1, Messages: 5}) {
		t.Fatalf("Run = %+v, %v; want 1 room, its member count and 5 messages", rep, err)
	}
	assertMarked(t, spy.marked(), append([]store.StateTarget{roomMark(staleRoom, store.BitMemberCount, 1)}, timelineMarks(staleRoom, 5, 1)...))
	opts.Tenant = "other"
	if _, err := resync.Run(t.Context(), w.deps(spy), opts); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("Run(room of another tenant) = %v, want ErrInvalidArgument", err)
	}
}

func TestResyncDryRunCountsWithoutMarkingOrPacing(t *testing.T) {
	w := newWorld(t)
	w.react(t, busyRoom, 40, "bob", "👍", lostFrom.Add(5*time.Minute))
	spy := w.spy()
	rep, err := resync.Run(t.Context(), w.deps(spy), resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: 1, DryRun: true})
	want := resync.Report{Rooms: 2, RoomsCreated: 1, MemberCounts: 2, Messages: 61, Reactions: 1, Members: 1, CountChecks: 1, DryRun: true}
	if err != nil || rep != want || len(spy.pages) != 0 || len(spy.checks) != 0 {
		t.Fatalf("dry run = %+v, %v with %d marks and %d checks; want %+v and nothing written", rep, err, len(spy.pages), len(spy.checks), want)
	}
}

func TestResyncIsPacedByRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		spy := w.spy()
		start := time.Now()
		opts := resync.Options{From: lostFrom.Add(-4 * time.Hour), To: lostFrom.Add(-2 * time.Hour), Room: staleRoom, Rate: 10}
		rep, err := resync.Run(t.Context(), w.deps(spy), opts)
		if err != nil || rep.Messages != 5 || rep.MemberCounts != 1 {
			t.Fatalf("Run = %+v, %v; want 5 messages and 1 member count", rep, err)
		}
		if took := time.Since(start); took != 600*time.Millisecond {
			t.Fatalf("6 targets at 10/s took %v, want 600ms", took)
		}
	})
}

func TestResyncStopsAtTheFirstMarkError(t *testing.T) {
	w := newWorld(t)
	spy := w.spy()
	spy.err = errMark
	rep, err := resync.Run(t.Context(), w.deps(spy), resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if !errors.Is(err, errMark) || rep.MemberCounts != 0 || rep.Messages != 0 || rep.RoomsCreated != 0 {
		t.Fatalf("Run = %+v, %v; want errMark and nothing counted", rep, err)
	}
}

func pageSizes(spy *markSpy) []int {
	sizes := make([]int, len(spy.pages))
	for i, p := range spy.pages {
		sizes[i] = len(p)
	}
	return sizes
}
