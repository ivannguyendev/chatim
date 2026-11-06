package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/pkg/core/mongoindex"
)

const (
	uploadsCollection   = "media_uploads"
	namespaceExistsCode = 48
	duplicateKeyCode    = 11000
)

var ErrNotClustered = errors.New("mongostore: collection is not clustered on _id")

func Indexes() mongoindex.Catalog {
	return mongoindex.Catalog{Collections: []mongoindex.Collection{{Name: uploadsCollection}}}
}

func Bootstrap(ctx context.Context, db *mongo.Database, reporters ...mongoindex.Reporter) error {
	opts := options.CreateCollection().
		SetClusteredIndex(bson.D{{Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "unique", Value: true}}).
		SetStorageEngine(bson.D{{Key: "wiredTiger", Value: bson.D{{Key: "configString", Value: "block_compressor=zstd"}}}})
	if err := db.CreateCollection(ctx, uploadsCollection, opts); err != nil && !hasCode(err, namespaceExistsCode) {
		return fmt.Errorf("bootstrap %s: create collection: %w", uploadsCollection, err)
	}
	specs, err := db.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: uploadsCollection}})
	if err != nil {
		return fmt.Errorf("bootstrap %s: list collections: %w", uploadsCollection, err)
	}
	if len(specs) != 1 {
		return fmt.Errorf("bootstrap %s: %w", uploadsCollection, ErrNotClustered)
	}
	if dir, ok := specs[0].Options.Lookup("clusteredIndex", "key", "_id").AsInt64OK(); !ok || dir != 1 {
		return fmt.Errorf("bootstrap %s: %w", uploadsCollection, ErrNotClustered)
	}
	return mongoindex.Ensure(ctx, db, Indexes(), reporters...)
}

func hasCode(err error, code int) bool {
	se, ok := errors.AsType[mongo.ServerError](err)
	return ok && se.HasErrorCode(code)
}
