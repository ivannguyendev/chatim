package mongostore

import (
	"math"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const (
	stateField        = "_state"
	stateAtField      = "_state_at"
	clearedAtField    = "cleared_at"
	statePendingIndex = "state_pending"
	stateClearBatch   = 500
)

var stateExists = bson.E{Key: stateField, Value: bson.D{{Key: "$exists", Value: true}}}

type stateBatch struct {
	coll    store.StateCollection
	bit     store.StateBit
	targets []store.StateTarget
}

func stateBatches(targets []store.StateTarget) []stateBatch {
	sorted := slices.Clone(targets)
	slices.SortStableFunc(sorted, func(a, b store.StateTarget) int {
		if a.Collection != b.Collection {
			return int(a.Collection) - int(b.Collection)
		}
		return int(a.Bit) - int(b.Bit)
	})
	var out []stateBatch
	for _, t := range sorted {
		n := len(out)
		if n == 0 || out[n-1].coll != t.Collection || out[n-1].bit != t.Bit || len(out[n-1].targets) == stateClearBatch {
			out = append(out, stateBatch{coll: t.Collection, bit: t.Bit})
			n++
		}
		out[n-1].targets = append(out[n-1].targets, t)
	}
	return out
}

func bitIsSet(bit store.StateBit) bson.E {
	return bson.E{Key: stateField, Value: bson.D{{Key: "$bitsAllSet", Value: bitValue(bit)}}}
}

func bitValue(bit store.StateBit) int32 {
	if bit > math.MaxInt32 {
		return 0
	}
	return int32(bit)
}

func stateFilter(c store.StateCollection, bit store.StateBit, targets []store.StateTarget, extra ...bson.E) (bson.D, bool) {
	_, versioned := versionField(c, bit)
	switch {
	case c == store.StateHidden:
		return orOf(matches(targets, hiddenMatch, extra))
	case c == store.StateRooms && !versioned:
		return roomIDsFilter(targets, extra)
	case c == store.StateRooms:
		return orOf(matches(targets, roomVersionMatch, extra))
	case versioned:
		return orOf(matches(targets, keyVersionMatch, extra))
	default:
		ids := make([][]byte, len(targets))
		for i, t := range targets {
			ids[i] = stateKey(t)
		}
		return boundedIDs(ids, extra...), len(ids) > 0
	}
}

func matches(targets []store.StateTarget, match func(store.StateTarget) (bson.D, bool), extra []bson.E) []bson.D {
	out := make([]bson.D, 0, len(targets))
	for _, t := range targets {
		if m, ok := match(t); ok {
			out = append(out, append(m, extra...))
		}
	}
	return out
}

func orOf(branches []bson.D) (bson.D, bool) {
	switch len(branches) {
	case 0:
		return nil, false
	case 1:
		return branches[0], true
	}
	or := make(bson.A, len(branches))
	for i, b := range branches {
		or[i] = b
	}
	return bson.D{{Key: "$or", Value: or}}, true
}

func roomIDsFilter(targets []store.StateTarget, extra []bson.E) (bson.D, bool) {
	ids := make(bson.A, 0, len(targets))
	for _, t := range targets {
		if id, ok := int64Of(t.Room); ok {
			ids = append(ids, id)
		}
	}
	return append(bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, extra...), len(ids) > 0
}

func roomVersionMatch(t store.StateTarget) (bson.D, bool) {
	id, ok := int64Of(t.Room)
	version, okVer := versionMatch(t)
	return bson.D{{Key: "_id", Value: id}, version}, ok && okVer
}

func keyVersionMatch(t store.StateTarget) (bson.D, bool) {
	k := stateKey(t)
	version, ok := versionMatch(t)
	return bson.D{{Key: "_id", Value: bson.D{{Key: "$gte", Value: k}, {Key: "$lte", Value: k}}}, version}, ok
}

func hiddenMatch(t store.StateTarget) (bson.D, bool) {
	room, okRoom := int64Of(t.Room)
	thread, okThread := int64Of(t.Thread)
	seq, okSeq := int64Of(t.Seq)
	m := bson.D{{Key: "user_id", Value: t.User}, {Key: "room_id", Value: room}, {Key: "thread_root", Value: thread}, {Key: "seq", Value: seq}}
	return m, okRoom && okThread && okSeq
}

func stateKey(t store.StateTarget) []byte {
	switch t.Collection {
	case store.StateEdits:
		return keys.Edit(t.Room, t.Thread, t.Seq, uint32(min(t.Ver, math.MaxUint32)))
	case store.StateInteractions:
		return keys.InteractionUser(keys.Msg(t.Room, t.Thread, t.Seq), t.Kind, t.User)
	case store.StatePins:
		return keys.Pin(t.Room, t.Seq)
	case store.StateMembers:
		return keys.Member(t.Room, t.User)
	default:
		return keys.Msg(t.Room, t.Thread, t.Seq)
	}
}

func int64Of(v uint64) (int64, bool) {
	if v > math.MaxInt64 {
		return 0, false
	}
	return int64(v), true
}
