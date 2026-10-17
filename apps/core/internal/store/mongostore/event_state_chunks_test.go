package mongostore

import (
	"reflect"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func clearTarget(room, seq uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: room, Seq: seq}
}

func spreadTargets(runRooms, runLength, scattered int) []store.StateTarget {
	var points []store.StateTarget
	for i := range scattered {
		points = append(points, clearTarget(50, uint64(2*i+1)))
	}
	var out []store.StateTarget
	for seq := 1; seq <= runLength; seq++ {
		share := points[:min(len(points), scattered/runLength)]
		points = points[len(share):]
		out = append(out, share...)
		for room := 1; room <= runRooms; room++ {
			out = append(out, clearTarget(uint64(room), uint64(seq)))
		}
	}
	return append(out, points...)
}

func TestStateClearsOfAFlushedSetMakeOneRangePerRoomRunAcrossChunks(t *testing.T) {
	const rooms, run, scattered = 10, 4, 1203
	set := bitIsSet(store.BitMsgCreated)
	batches := stateBatches(spreadTargets(rooms, run, scattered))
	if len(batches) != 1 {
		t.Fatalf("%d batches, want one per collection and bit", len(batches))
	}
	writes := stateWrites(batches[0].coll, batches[0].bit, batches[0].targets, set)
	var ranges []stateWrite
	points := 0
	for _, w := range writes {
		if w.many {
			ranges = append(ranges, w)
			continue
		}
		points++
	}
	if len(ranges) != rooms || points != scattered {
		t.Fatalf("%d range and %d point statements, want %d and %d", len(ranges), points, rooms, scattered)
	}
	for i, w := range ranges {
		room := uint64(i + 1)
		want := stateRange(keys.Msg(room, 0, 1), keys.Msg(room, 0, run), set)
		expectWrites(t, "room run", []stateWrite{w}, []stateWrite{want})
	}
	chunks := stateChunks(writes)
	if len(chunks) != (len(writes)+stateBulkMax-1)/stateBulkMax {
		t.Fatalf("%d chunks of %d statements", len(chunks), len(writes))
	}
	for i, c := range chunks {
		if len(c) == 0 || len(c) > stateBulkMax {
			t.Fatalf("chunk %d holds %d statements, want 1..%d", i, len(c), stateBulkMax)
		}
	}
	if !reflect.DeepEqual(slices.Concat(chunks...), writes) {
		t.Fatal("chunks do not keep every statement in order")
	}
}
