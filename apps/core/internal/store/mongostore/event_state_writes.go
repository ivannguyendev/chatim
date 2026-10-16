package mongostore

import (
	"bytes"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type stateWrite struct {
	filter bson.D
	many   bool
}

func (w stateWrite) model(update mongo.Pipeline) mongo.WriteModel {
	if w.many {
		return mongo.NewUpdateManyModel().SetFilter(w.filter).SetUpdate(update)
	}
	return mongo.NewUpdateOneModel().SetFilter(w.filter).SetUpdate(update)
}

func stateWrites(c store.StateCollection, bit store.StateBit, targets []store.StateTarget, extra ...bson.E) []stateWrite {
	_, versioned := versionField(c, bit)
	switch {
	case c == store.StateHidden:
		return pointWrites(targets, hiddenMatch, extra)
	case c == store.StateRooms && !versioned:
		return pointWrites(targets, roomMatch, extra)
	case c == store.StateRooms:
		return pointWrites(targets, roomVersionMatch, extra)
	case versioned:
		return pointWrites(targets, keyVersionMatch, extra)
	default:
		return runWrites(targets, extra)
	}
}

func pointWrites(targets []store.StateTarget, match func(store.StateTarget) (bson.D, bool), extra []bson.E) []stateWrite {
	out := make([]stateWrite, 0, len(targets))
	for _, t := range targets {
		if m, ok := match(t); ok {
			out = append(out, stateWrite{filter: append(m, extra...)})
		}
	}
	return out
}

func runWrites(targets []store.StateTarget, extra []bson.E) []stateWrite {
	ids := make([][]byte, len(targets))
	for i, t := range targets {
		ids[i] = stateKey(t)
	}
	runs := idRuns(ids)
	out := make([]stateWrite, len(runs))
	for i, r := range runs {
		if bytes.Equal(r.lo, r.hi) {
			out[i] = stateWrite{filter: append(bson.D{{Key: "_id", Value: r.lo}}, extra...)}
			continue
		}
		out[i] = stateWrite{filter: r.filter(extra), many: true}
	}
	return out
}

func stateChunks(writes []stateWrite) [][]stateWrite {
	return slices.Collect(slices.Chunk(writes, stateBulkMax))
}
