package mongostore

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

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
