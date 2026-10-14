package mongostore

import (
	"bytes"
	"cmp"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type idRun struct{ lo, hi []byte }

func boundedIDs(ids [][]byte, extra ...bson.E) bson.D {
	runs := idRuns(ids)
	if len(runs) == 0 {
		return append(bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{}}}}}, extra...)
	}
	if len(runs) == 1 && bytes.Equal(runs[0].lo, runs[0].hi) {
		return append(bson.D{{Key: "_id", Value: runs[0].lo}}, extra...)
	}
	if len(runs) == 1 {
		return runs[0].filter(extra)
	}
	or := make(bson.A, len(runs))
	for i, r := range runs {
		or[i] = r.filter(extra)
	}
	return bson.D{{Key: "$or", Value: or}}
}

func (r idRun) filter(extra []bson.E) bson.D {
	bounds := bson.D{{Key: "$gte", Value: r.lo}, {Key: "$lte", Value: r.hi}}
	return append(bson.D{{Key: "_id", Value: bounds}}, extra...)
}

func idRuns(ids [][]byte) []idRun {
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, compareBinData)
	sorted = slices.CompactFunc(sorted, bytes.Equal)
	var runs []idRun
	for _, id := range sorted {
		if n := len(runs); n > 0 && isSuccessor(runs[n-1].hi, id) {
			runs[n-1].hi = id
			continue
		}
		runs = append(runs, idRun{lo: id, hi: id})
	}
	return runs
}

func compareBinData(a, b []byte) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), bytes.Compare(a, b))
}

func isSuccessor(prev, next []byte) bool {
	if len(prev) != len(next) {
		return false
	}
	succ := bytes.Clone(prev)
	for i := len(succ) - 1; i >= 0; i-- {
		if succ[i] < 0xff {
			succ[i]++
			return bytes.Equal(succ, next)
		}
		succ[i] = 0
	}
	return false
}
