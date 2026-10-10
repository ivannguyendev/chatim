package mongostore

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func reactionDeltaPipeline(deltas []store.EmojiDelta) mongo.Pipeline {
	lit := make(bson.A, len(deltas))
	for i, d := range deltas {
		lit[i] = bson.D{{Key: "e", Value: d.Emoji}, {Key: "d", Value: int64(d.Delta)}}
	}
	given := bson.D{{Key: "$literal", Value: lit}}
	counts := bson.D{
		{Key: "c", Value: bson.D{{Key: "$sortArray", Value: bson.D{
			{Key: "input", Value: positive(bson.D{{Key: "$concatArrays", Value: bson.A{movedCounts(given), newCounts(given)}}})},
			{Key: "sortBy", Value: bson.D{{Key: "n", Value: -1}, {Key: "e", Value: 1}}},
		}}}},
		{Key: "v", Value: bumped("$rx.v")},
	}
	return mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "rx", Value: counts}}}}}
}

func movedCounts(given bson.D) bson.D {
	sum := bson.D{{Key: "$reduce", Value: bson.D{
		{Key: "input", Value: given},
		{Key: "initialValue", Value: int64(0)},
		{Key: "in", Value: bson.D{{Key: "$add", Value: bson.A{"$$value", bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$$this.e", "$$x.e"}}}, "$$this.d", int64(0),
		}}}}}}},
	}}}
	return bson.D{{Key: "$map", Value: bson.D{
		{Key: "input", Value: orEmpty("$rx.c")},
		{Key: "as", Value: "x"},
		{Key: "in", Value: bson.D{{Key: "e", Value: "$$x.e"}, {Key: "n", Value: bson.D{{Key: "$add", Value: bson.A{"$$x.n", sum}}}}}},
	}}}
}

func newCounts(given bson.D) bson.D {
	absent := bson.D{{Key: "$filter", Value: bson.D{
		{Key: "input", Value: given},
		{Key: "as", Value: "y"},
		{Key: "cond", Value: bson.D{{Key: "$not", Value: bson.A{bson.D{{Key: "$in", Value: bson.A{"$$y.e", orEmpty("$rx.c.e")}}}}}}},
	}}}
	return bson.D{{Key: "$map", Value: bson.D{
		{Key: "input", Value: absent},
		{Key: "as", Value: "y"},
		{Key: "in", Value: bson.D{{Key: "e", Value: "$$y.e"}, {Key: "n", Value: "$$y.d"}}},
	}}}
}

func positive(counts bson.D) bson.D {
	return bson.D{{Key: "$filter", Value: bson.D{
		{Key: "input", Value: counts},
		{Key: "as", Value: "z"},
		{Key: "cond", Value: bson.D{{Key: "$gt", Value: bson.A{"$$z.n", int64(0)}}}},
	}}}
}

func replyDeltaPipeline(delta int) mongo.Pipeline {
	n := bson.D{{Key: "$max", Value: bson.A{int64(0), bson.D{{Key: "$add", Value: bson.A{orZero("$rc.n"), int64(delta)}}}}}}
	count := bson.D{{Key: "n", Value: n}, {Key: "v", Value: bumped("$rc.v")}}
	return mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "rc", Value: count}}}}}
}

func bumped(field string) bson.D {
	return bson.D{{Key: "$add", Value: bson.A{orZero(field), int64(1)}}}
}

func orZero(field string) bson.D { return bson.D{{Key: "$ifNull", Value: bson.A{field, int64(0)}}} }

func orEmpty(field string) bson.D { return bson.D{{Key: "$ifNull", Value: bson.A{field, bson.A{}}}} }
