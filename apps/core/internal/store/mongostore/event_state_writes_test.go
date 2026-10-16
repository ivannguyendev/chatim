package mongostore

import (
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func one(filter ...bson.E) stateWrite { return stateWrite{filter: bson.D(filter)} }

func stateID(k any) bson.E { return bson.E{Key: "_id", Value: k} }

func stateRange(lo, hi []byte, extra ...bson.E) stateWrite {
	bounds := bson.D{{Key: "$gte", Value: lo}, {Key: "$lte", Value: hi}}
	return stateWrite{filter: append(bson.D{{Key: "_id", Value: bounds}}, extra...), many: true}
}

func expectWrites(t *testing.T, name string, got, want []stateWrite) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s writes = %v, want %v", name, got, want)
	}
	for _, w := range got {
		if hasOr(w.filter) {
			t.Fatalf("%s filter %v holds an $or", name, w.filter)
		}
	}
}

func hasOr(v any) bool {
	switch x := v.(type) {
	case bson.D:
		return slices.ContainsFunc(x, func(e bson.E) bool { return e.Key == "$or" || hasOr(e.Value) })
	case bson.A:
		return slices.ContainsFunc(x, hasOr)
	}
	return false
}

func TestStateWritesOfInsertOnceKeysAreOneRangePerRunAndOnePointPerKey(t *testing.T) {
	set := bitIsSet(store.BitMsgCreated)
	targets := []store.StateTarget{filterMsg(5, 1, 0), filterMsg(1, 1, 0), filterMsg(2, 1, 0), filterMsg(3, 1, 0), filterMsg(9, 1, 0)}
	expectWrites(t, "messages bit 1", stateWrites(store.StateMessages, store.BitMsgCreated, targets, set), []stateWrite{
		stateRange(keys.Msg(9, 0, 1), keys.Msg(9, 0, 3), set),
		one(stateID(keys.Msg(9, 0, 5)), set),
		one(stateID(keys.Msg(9, 0, 9)), set),
	})
	edit := store.StateTarget{Collection: store.StateEdits, Bit: 1, Room: 9, Seq: 4, Ver: 2}
	expectWrites(t, "one edit", stateWrites(store.StateEdits, 1, []store.StateTarget{edit}), []stateWrite{one(stateID(keys.Edit(9, 0, 4, 2)))})
	pins := []store.StateTarget{{Collection: store.StatePins, Bit: 1, Room: 9, Seq: 7}, {Collection: store.StatePins, Bit: 1, Room: 9, Seq: 6}}
	expectWrites(t, "two pins", stateWrites(store.StatePins, 1, pins), []stateWrite{stateRange(keys.Pin(9, 6), keys.Pin(9, 7))})
}

func TestStateWritesOfVersionedTargetsAreOnePointWritePerTarget(t *testing.T) {
	set := bitIsSet(store.BitReadPosition)
	bob := store.StateTarget{Collection: store.StateMembers, Bit: store.BitReadPosition, Room: 9, User: "bob", Ver: 7}
	ann := bob
	ann.User, ann.Ver = "ann", 0
	zero := bson.E{Key: "read_ver", Value: bson.D{{Key: "$in", Value: bson.A{int64(0), nil}}}}
	expectWrites(t, "two members", stateWrites(store.StateMembers, store.BitReadPosition, []store.StateTarget{bob, ann}, set), []stateWrite{
		one(stateID(keys.Member(9, "bob")), bson.E{Key: "read_ver", Value: int64(7)}, set),
		one(stateID(keys.Member(9, "ann")), zero, set),
	})
	at := time.Date(2026, 10, 16, 1, 2, 3, 456_789_000, time.UTC)
	cleared := store.StateTarget{Collection: store.StateMembers, Bit: store.BitHistoryCleared, Room: 9, User: "bob", At: at}
	expectWrites(t, "cleared", stateWrites(store.StateMembers, store.BitHistoryCleared, []store.StateTarget{cleared}),
		[]stateWrite{one(stateID(keys.Member(9, "bob")), bson.E{Key: "cleared_at", Value: at.Truncate(time.Millisecond)})})
	rx := []store.StateTarget{filterMsg(3, store.BitReactionCounts, 4), filterMsg(4, store.BitReactionCounts, 1)}
	expectWrites(t, "reactions", stateWrites(store.StateMessages, store.BitReactionCounts, rx), []stateWrite{
		one(stateID(keys.Msg(9, 0, 3)), bson.E{Key: "rx.v", Value: int64(4)}),
		one(stateID(keys.Msg(9, 0, 4)), bson.E{Key: "rx.v", Value: int64(1)}),
	})
}

func TestStateWritesSkipVersionsOutOfRange(t *testing.T) {
	huge := filterMsg(3, store.BitReplyCounts, math.MaxInt64+1)
	if got := stateWrites(store.StateMessages, store.BitReplyCounts, []store.StateTarget{huge}); len(got) != 0 {
		t.Fatalf("writes of an unstorable version = %v, want none", got)
	}
	got := stateWrites(store.StateMessages, store.BitReplyCounts, []store.StateTarget{huge, filterMsg(4, store.BitReplyCounts, 2)})
	expectWrites(t, "mixed", got, []stateWrite{one(stateID(keys.Msg(9, 0, 4)), bson.E{Key: "rc.v", Value: int64(2)})})
}

func TestStateWritesOfRoomsAndHiddenUseTheirKeys(t *testing.T) {
	set := bitIsSet(1)
	created := []store.StateTarget{{Collection: store.StateRooms, Bit: 1, Room: 9}, {Collection: store.StateRooms, Bit: 1, Room: 4}}
	expectWrites(t, "rooms created", stateWrites(store.StateRooms, store.BitRoomCreated, created, set),
		[]stateWrite{one(stateID(int64(9)), set), one(stateID(int64(4)), set)})
	count := store.StateTarget{Collection: store.StateRooms, Bit: store.BitMemberCount, Room: 9, Ver: 3}
	expectWrites(t, "member count", stateWrites(store.StateRooms, store.BitMemberCount, []store.StateTarget{count}),
		[]stateWrite{one(stateID(int64(9)), bson.E{Key: "member_count_ver", Value: int64(3)})})
	hide := store.StateTarget{Collection: store.StateHidden, Bit: 1, Room: 9, Thread: 7, Seq: 2, User: "bob"}
	want := one(bson.E{Key: "user_id", Value: "bob"}, bson.E{Key: "room_id", Value: int64(9)}, bson.E{Key: "thread_root", Value: int64(7)}, bson.E{Key: "seq", Value: int64(2)}, set)
	expectWrites(t, "hidden", stateWrites(store.StateHidden, 1, []store.StateTarget{hide}, set), []stateWrite{want})
}

func TestStateWritesOfAFullBatchNeverHoldAnOr(t *testing.T) {
	var targets []store.StateTarget
	for room := uint64(1); room <= 50; room++ {
		for seq := uint64(1); seq <= stateClearBatch/50; seq += 2 {
			targets = append(targets, store.StateTarget{Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: room, Seq: seq})
		}
	}
	for _, b := range stateBatches(targets) {
		writes := stateWrites(b.coll, b.bit, b.targets, bitIsSet(b.bit))
		if len(writes) != len(b.targets) {
			t.Fatalf("%d scattered keys gave %d writes, want one each", len(b.targets), len(writes))
		}
		for _, w := range writes {
			if hasOr(w.filter) || w.many {
				t.Fatalf("write %+v; want a point write with no $or", w)
			}
		}
	}
}
