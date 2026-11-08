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
	uploadsCollection      = "media_uploads"
	messageFilesCollection = "media_message_files"
	namespaceExistsCode    = 48
	duplicateKeyCode       = 11000
)

var (
	ErrNotClustered = errors.New("mongostore: collection is not clustered on _id")
	uploadsByOwner  = bson.D{{Key: "tenant_id", Value: 1}, {Key: "user_id", Value: 1}, {Key: "state", Value: 1}, {Key: "_id", Value: -1}}
	filesByMessage  = bson.D{{Key: "tenant_id", Value: 1}, {Key: "message_key", Value: 1}, {Key: "state", Value: 1}}
	filesByKey      = bson.D{{Key: "file_key", Value: 1}, {Key: "state", Value: 1}, {Key: "message_key", Value: 1}}
)

func Indexes() mongoindex.Catalog {
	return mongoindex.Catalog{Collections: []mongoindex.Collection{
		{Name: uploadsCollection, Indexes: []mongo.IndexModel{{Keys: uploadsByOwner}}},
		{Name: messageFilesCollection, Indexes: []mongo.IndexModel{{Keys: filesByMessage}, {Keys: filesByKey}}},
	}}
}

func Bootstrap(ctx context.Context, db *mongo.Database, reporters ...mongoindex.Reporter) error {
	for _, name := range []string{uploadsCollection, messageFilesCollection} {
		if err := createClustered(ctx, db, name); err != nil {
			return err
		}
	}
	return mongoindex.Ensure(ctx, db, Indexes(), reporters...)
}

func createClustered(ctx context.Context, db *mongo.Database, name string) error {
	opts := options.CreateCollection().
		SetClusteredIndex(bson.D{{Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "unique", Value: true}}).
		SetStorageEngine(bson.D{{Key: "wiredTiger", Value: bson.D{{Key: "configString", Value: "block_compressor=zstd"}}}})
	if err := db.CreateCollection(ctx, name, opts); err != nil && !hasCode(err, namespaceExistsCode) {
		return fmt.Errorf("bootstrap %s: create collection: %w", name, err)
	}
	specs, err := db.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: name}})
	if err != nil {
		return fmt.Errorf("bootstrap %s: list collections: %w", name, err)
	}
	if len(specs) != 1 {
		return fmt.Errorf("bootstrap %s: %w", name, ErrNotClustered)
	}
	if dir, ok := specs[0].Options.Lookup("clusteredIndex", "key", "_id").AsInt64OK(); !ok || dir != 1 {
		return fmt.Errorf("bootstrap %s: %w", name, ErrNotClustered)
	}
	return nil
}

func hasCode(err error, code int) bool {
	se, ok := errors.AsType[mongo.ServerError](err)
	return ok && se.HasErrorCode(code)
}
