package mongostore

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func OplogWindow(ctx context.Context, client *mongo.Client) (time.Duration, error) {
	oplog := client.Database("local").Collection("oplog.rs")
	first, err := oplogTime(ctx, oplog, 1)
	if err != nil {
		return 0, err
	}
	last, err := oplogTime(ctx, oplog, -1)
	if err != nil {
		return 0, err
	}
	return time.Duration(last.T-first.T) * time.Second, nil
}

func oplogTime(ctx context.Context, oplog *mongo.Collection, direction int) (bson.Timestamp, error) {
	var doc struct {
		TS bson.Timestamp `bson:"ts"`
	}
	opts := options.FindOne().SetSort(bson.D{{Key: "$natural", Value: direction}}).SetProjection(bson.D{{Key: "ts", Value: 1}})
	err := oplog.FindOne(ctx, bson.D{}, opts).Decode(&doc)
	return doc.TS, err
}
