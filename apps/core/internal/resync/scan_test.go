package resync_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestResyncPublishesRecordsOfTheLostRangeOnly(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var want []string
	for seq := uint64(91); seq >= 31; seq-- {
		want = append(want, work.Record{Kind: store.MessageInserted, Room: busyRoom, Seq: seq}.ID())
	}
	want = append(want, work.Record{Kind: store.RoomInserted, Room: newRoom}.ID(), memberID(newRoom, "alice", 1))
	if got := pub.published(); !slices.Equal(got, want) {
		t.Fatalf("published %d ids %v,\nwant %d ids %v", len(got), got, len(want), want)
	}
	if rep != (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, MemberRecords: 1}) {
		t.Fatalf("report = %+v, want 2 rooms, 1 room record, 61 message records, 1 member record", rep)
	}
}

func TestResyncOfOneRoomSkipsTheActivityIndex(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{}
	opts := resync.Options{From: lostFrom.Add(-4 * time.Hour), To: lostFrom.Add(-2 * time.Hour), Room: staleRoom, Rate: resync.MaxRate}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, opts)
	if err != nil || rep != (resync.Report{Rooms: 1, MessageRecords: 5}) || len(pub.published()) != 5 {
		t.Fatalf("Run = %+v, %v with %d published; want 1 room and 5 message records", rep, err, len(pub.published()))
	}
	opts.Tenant = "other"
	if _, err := resync.Run(t.Context(), w.deps(pub), target, opts); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("Run(room of another tenant) = %v, want ErrInvalidArgument", err)
	}
}

func TestResyncDryRunCountsWithoutPublishingOrPacing(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: 1, DryRun: true})
	if err != nil || rep != (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, MemberRecords: 1, DryRun: true}) || len(pub.published()) != 0 {
		t.Fatalf("dry run = %+v, %v with %d published; want counts only", rep, err, len(pub.published()))
	}
}

func TestResyncIsPacedByRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		pub := &publishSpy{}
		start := time.Now()
		opts := resync.Options{From: lostFrom.Add(-4 * time.Hour), To: lostFrom.Add(-2 * time.Hour), Room: staleRoom, Rate: 10}
		rep, err := resync.Run(t.Context(), w.deps(pub), target, opts)
		if err != nil || rep.MessageRecords != 5 {
			t.Fatalf("Run = %+v, %v; want 5 message records", rep, err)
		}
		if took := time.Since(start); took != 500*time.Millisecond {
			t.Fatalf("5 records at 10/s took %v, want 500ms", took)
		}
	})
}

func TestResyncStopsAtTheFirstPublishError(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{err: errPublish}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if !errors.Is(err, errPublish) || rep.MessageRecords != 0 || rep.RoomRecords != 0 {
		t.Fatalf("Run = %+v, %v; want errPublish and nothing counted", rep, err)
	}
}
