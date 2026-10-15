package mongostore

import (
	"math"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func filterMsg(seq uint64, bit store.StateBit, ver uint64) store.StateTarget {
	return store.StateTarget{Collection: store.StateMessages, Bit: bit, Room: 9, Seq: seq, Ver: ver}
}

func stateKeyRange(k []byte, extra ...bson.E) bson.D {
	return append(bson.D{{Key: "_id", Value: bson.D{{Key: "$gte", Value: k}, {Key: "$lte", Value: k}}}}, extra...)
}

func expectStateFilter(t *testing.T, name string, got bson.D, ok bool, want bson.D) {
	t.Helper()
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s filter = %v (ok %v), want %v", name, got, ok, want)
	}
}

func TestStateBatchesGroupByCollectionAndBitInChunks(t *testing.T) {
	var targets []store.StateTarget
	for seq := uint64(1); seq <= stateClearBatch+1; seq++ {
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
		{int(store.StateMessages), int(store.BitMsgCreated), stateClearBatch},
		{int(store.StateMessages), int(store.BitMsgCreated), 1},
		{int(store.StateMessages), int(store.BitReactionCounts), 1},
		{int(store.StateRooms), int(store.BitRoomCreated), 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
}

func TestStateFilterOfInsertOnceKeysUsesBoundedRanges(t *testing.T) {
	targets := []store.StateTarget{filterMsg(5, 1, 0), filterMsg(1, 1, 0), filterMsg(2, 1, 0)}
	got, ok := stateFilter(store.StateMessages, store.BitMsgCreated, targets, bitIsSet(store.BitMsgCreated))
	set := bitIsSet(store.BitMsgCreated)
	want := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "_id", Value: bson.D{{Key: "$gte", Value: keys.Msg(9, 0, 1)}, {Key: "$lte", Value: keys.Msg(9, 0, 2)}}}, set},
		bson.D{{Key: "_id", Value: bson.D{{Key: "$gte", Value: keys.Msg(9, 0, 5)}, {Key: "$lte", Value: keys.Msg(9, 0, 5)}}}, set},
	}}}
	expectStateFilter(t, "messages bit 1", got, ok, want)
	edit := store.StateTarget{Collection: store.StateEdits, Bit: 1, Room: 9, Seq: 4, Ver: 2}
	got, ok = stateFilter(store.StateEdits, 1, []store.StateTarget{edit})
	expectStateFilter(t, "one edit", got, ok, bson.D{{Key: "_id", Value: keys.Edit(9, 0, 4, 2)}})
	pin := store.StateTarget{Collection: store.StatePins, Bit: 1, Room: 9, Seq: 6}
	got, ok = stateFilter(store.StatePins, 1, []store.StateTarget{pin})
	expectStateFilter(t, "one pin", got, ok, bson.D{{Key: "_id", Value: keys.Pin(9, 6)}})
}

func TestStateFilterOfVersionedTargetsHasOneBoundedBranchPerTarget(t *testing.T) {
	set := bitIsSet(store.BitReadPosition)
	bob := store.StateTarget{Collection: store.StateMembers, Bit: store.BitReadPosition, Room: 9, User: "bob", Ver: 7}
	got, ok := stateFilter(store.StateMembers, store.BitReadPosition, []store.StateTarget{bob}, set)
	expectStateFilter(t, "one member", got, ok, stateKeyRange(keys.Member(9, "bob"), bson.E{Key: "read_ver", Value: int64(7)}, set))
	ann := bob
	ann.User, ann.Ver = "ann", 0
	got, ok = stateFilter(store.StateMembers, store.BitReadPosition, []store.StateTarget{bob, ann}, set)
	zero := bson.E{Key: "read_ver", Value: bson.D{{Key: "$in", Value: bson.A{int64(0), nil}}}}
	want := bson.D{{Key: "$or", Value: bson.A{
		stateKeyRange(keys.Member(9, "bob"), bson.E{Key: "read_ver", Value: int64(7)}, set),
		stateKeyRange(keys.Member(9, "ann"), zero, set),
	}}}
	expectStateFilter(t, "two members", got, ok, want)
	at := time.Date(2026, 10, 16, 1, 2, 3, 456_789_000, time.UTC)
	cleared := store.StateTarget{Collection: store.StateMembers, Bit: store.BitHistoryCleared, Room: 9, User: "bob", At: at}
	got, ok = stateFilter(store.StateMembers, store.BitHistoryCleared, []store.StateTarget{cleared})
	expectStateFilter(t, "cleared", got, ok, stateKeyRange(keys.Member(9, "bob"), bson.E{Key: "cleared_at", Value: at.Truncate(time.Millisecond)}))
	rx := filterMsg(3, store.BitReactionCounts, 4)
	got, ok = stateFilter(store.StateMessages, store.BitReactionCounts, []store.StateTarget{rx})
	expectStateFilter(t, "reactions", got, ok, stateKeyRange(keys.Msg(9, 0, 3), bson.E{Key: "rx.v", Value: int64(4)}))
}

func TestStateFilterSkipsVersionsOutOfRange(t *testing.T) {
	huge := filterMsg(3, store.BitReplyCounts, math.MaxInt64+1)
	if got, ok := stateFilter(store.StateMessages, store.BitReplyCounts, []store.StateTarget{huge}); ok {
		t.Fatalf("filter of an unstorable version = %v, want none", got)
	}
	got, ok := stateFilter(store.StateMessages, store.BitReplyCounts, []store.StateTarget{huge, filterMsg(4, store.BitReplyCounts, 2)})
	expectStateFilter(t, "mixed", got, ok, stateKeyRange(keys.Msg(9, 0, 4), bson.E{Key: "rc.v", Value: int64(2)}))
}

func TestStateFilterOfRoomsAndHidden(t *testing.T) {
	created := []store.StateTarget{{Collection: store.StateRooms, Bit: 1, Room: 9}, {Collection: store.StateRooms, Bit: 1, Room: 4}}
	got, ok := stateFilter(store.StateRooms, store.BitRoomCreated, created)
	expectStateFilter(t, "rooms created", got, ok, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{int64(9), int64(4)}}}}})
	count := store.StateTarget{Collection: store.StateRooms, Bit: store.BitMemberCount, Room: 9, Ver: 3}
	got, ok = stateFilter(store.StateRooms, store.BitMemberCount, []store.StateTarget{count})
	expectStateFilter(t, "member count", got, ok, bson.D{{Key: "_id", Value: int64(9)}, {Key: "member_count_ver", Value: int64(3)}})
	hide := store.StateTarget{Collection: store.StateHidden, Bit: 1, Room: 9, Thread: 7, Seq: 2, User: "bob"}
	set := bitIsSet(1)
	got, ok = stateFilter(store.StateHidden, 1, []store.StateTarget{hide}, set)
	want := bson.D{{Key: "user_id", Value: "bob"}, {Key: "room_id", Value: int64(9)}, {Key: "thread_root", Value: int64(7)}, {Key: "seq", Value: int64(2)}, set}
	expectStateFilter(t, "hidden", got, ok, want)
}
