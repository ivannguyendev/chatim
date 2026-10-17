package mongostore

import (
	"context"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const idSeparator = "│"

func tenantID(tenant, id string) string { return tenant + idSeparator + id }

func tenantRange(tenant string) bson.D {
	bounds := bson.D{{Key: "$gte", Value: tenant + idSeparator}, {Key: "$lt", Value: tenant + "┃"}}
	return bson.D{{Key: "_id", Value: bounds}, {Key: "tenant", Value: tenant}}
}

func newerUpsert(id string, ver int64, fields bson.D) mongo.WriteModel {
	newer := bson.D{{Key: "$lt", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$ver", int64(0)}}}, ver}}}
	set := make(bson.D, 0, len(fields)+1)
	for _, f := range slices.Concat(fields, bson.D{{Key: "ver", Value: ver}}) {
		set = append(set, bson.E{Key: f.Key, Value: bson.D{{Key: "$cond", Value: bson.A{newer, literal(f.Value), "$" + f.Key}}}})
	}
	return mongo.NewUpdateOneModel().
		SetFilter(bson.D{{Key: "_id", Value: id}}).
		SetUpdate(mongo.Pipeline{{{Key: "$set", Value: set}}}).
		SetUpsert(true)
}

func writeAll(ctx context.Context, coll *mongo.Collection, models []mongo.WriteModel) error {
	if len(models) == 0 {
		return nil
	}
	_, err := coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	return err
}
