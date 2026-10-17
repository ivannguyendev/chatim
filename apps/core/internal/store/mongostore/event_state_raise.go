package mongostore

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type flaggedDoc[T any] struct {
	Doc     T         `bson:",inline"`
	State   int32     `bson:"_state"`
	StateAt time.Time `bson:"_state_at"`
}

func flagDoc[T any](doc T, bit store.StateBit, at time.Time) flaggedDoc[T] {
	return flaggedDoc[T]{Doc: doc, State: bitValue(bit), StateAt: at}
}

func keptUnless(same, raised bson.D) bson.D {
	out := make(bson.D, len(raised))
	for i, e := range raised {
		out[i] = bson.E{Key: e.Key, Value: bson.D{{Key: "$cond", Value: bson.A{same, "$" + e.Key, e.Value}}}}
	}
	return out
}

func flaggedSet(bit store.StateBit, fields ...bson.E) mongo.Pipeline {
	set := append(bson.D{}, fields...)
	return mongo.Pipeline{{{Key: "$set", Value: append(set, raisedState(bit)...)}}}
}

func raisedState(bit store.StateBit) bson.D {
	state := bson.D{{Key: "$ifNull", Value: bson.A{"$" + stateField, int32(0)}}}
	at := bson.D{{Key: "$ifNull", Value: bson.A{"$" + stateAtField, "$$NOW"}}}
	return bson.D{
		{Key: stateField, Value: bson.D{{Key: "$bitOr", Value: bson.A{state, bitValue(bit)}}}},
		{Key: stateAtField, Value: bson.D{{Key: "$min", Value: bson.A{at, "$$NOW"}}}},
	}
}

func setLiteral(field string, value any) bson.E {
	return bson.E{Key: field, Value: bson.D{{Key: "$literal", Value: value}}}
}
