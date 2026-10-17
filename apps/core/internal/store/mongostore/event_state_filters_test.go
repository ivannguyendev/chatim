package mongostore

import (
	"reflect"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func filterMsg(seq uint64, bit store.StateBit, ver uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateMessages, Bit: bit, Room: 9, Seq: seq, Ver: ver}
}

func TestStateBatchesGroupByCollectionAndBitWithoutATargetCap(t *testing.T) {
	var targets []store.StateTarget
	for seq := uint64(1); seq <= 3*stateBulkMax+1; seq++ {
		targets = append(targets, filterMsg(seq, store.BitMsgCreated, 0))
	}
	room := store.StateTarget{Collection: store.StateRooms, Bit: store.BitRoomCreated, Room: 9}
	targets = append([]store.StateTarget{room, filterMsg(3, store.BitReactionCounts, 1)}, targets...)
	batches := stateBatches(targets)
	got := make([][3]int, len(batches))
	for i, b := range batches {
		got[i] = [3]int{int(b.coll), int(b.bit), len(b.targets)}
	}
	want := [][3]int{
		{int(store.StateMessages), int(store.BitMsgCreated), 3*stateBulkMax + 1},
		{int(store.StateMessages), int(store.BitReactionCounts), 1},
		{int(store.StateRooms), int(store.BitRoomCreated), 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
}
