package mongobound

import (
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Run[K []byte | string] struct{ Lo, Hi K }

func (r Run[K]) Filter(extra []bson.E) bson.D {
	bounds := bson.D{{Key: "$gte", Value: r.Lo}, {Key: "$lte", Value: r.Hi}}
	return append(bson.D{{Key: "_id", Value: bounds}}, extra...)
}

type keyOrder[K []byte | string] struct {
	compare   func(a, b K) int
	successor func(prev, next K) bool
}

func (o keyOrder[K]) filter(ids []K, extra []bson.E) bson.D {
	runs := o.runs(ids)
	if len(runs) == 0 {
		return append(bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{}}}}}, extra...)
	}
	if len(runs) == 1 && o.compare(runs[0].Lo, runs[0].Hi) == 0 {
		return append(bson.D{{Key: "_id", Value: runs[0].Lo}}, extra...)
	}
	if len(runs) == 1 {
		return runs[0].Filter(extra)
	}
	or := make(bson.A, len(runs))
	for i, r := range runs {
		or[i] = r.Filter(extra)
	}
	return bson.D{{Key: "$or", Value: or}}
}

func (o keyOrder[K]) runs(ids []K) []Run[K] {
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, o.compare)
	sorted = slices.CompactFunc(sorted, func(a, b K) bool { return o.compare(a, b) == 0 })
	var runs []Run[K]
	for _, id := range sorted {
		if n := len(runs); n > 0 && o.successor(runs[n-1].Hi, id) {
			runs[n-1].Hi = id
			continue
		}
		runs = append(runs, Run[K]{Lo: id, Hi: id})
	}
	return runs
}
