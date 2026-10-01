package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func Bootstrap(ctx context.Context, db *mongo.Database) error {
	if err := ensureMessages(ctx, db); err != nil {
		return err
	}
	for _, name := range []string{roomsCollection, membersCollection} {
		if err := createCollection(ctx, db, name); err != nil {
			return err
		}
	}
	return ensureMemberIndexes(ctx, db)
}

func ensureMessages(ctx context.Context, db *mongo.Database) error {
	opts := options.CreateCollection().
		SetClusteredIndex(bson.D{{Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "unique", Value: true}}).
		SetStorageEngine(bson.D{{Key: "wiredTiger", Value: bson.D{{Key: "configString", Value: "block_compressor=zstd"}}}})
	if err := createCollection(ctx, db, messagesCollection, opts); err != nil {
		return err
	}
	specs, err := db.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: messagesCollection}})
	if err != nil {
		return fmt.Errorf("bootstrap %s: list collections: %w", messagesCollection, err)
	}
	if len(specs) != 1 || !clusteredOnID(specs[0].Options) {
		return fmt.Errorf("bootstrap %s: %w", messagesCollection, ErrNotClustered)
	}
	return nil
}

func clusteredOnID(opts bson.Raw) bool {
	dir, ok := opts.Lookup("clusteredIndex", "key", "_id").AsInt64OK()
	return ok && dir == 1
}

func createCollection(ctx context.Context, db *mongo.Database, name string, opts ...options.Lister[options.CreateCollectionOptions]) error {
	if err := db.CreateCollection(ctx, name, opts...); err != nil && !namespaceExists(err) {
		return fmt.Errorf("bootstrap %s: create collection: %w", name, err)
	}
	return nil
}

func ensureMemberIndexes(ctx context.Context, db *mongo.Database) error {
	models := []mongo.IndexModel{
		{Keys: bson.D{{Key: "r", Value: 1}, {Key: "u", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "t", Value: 1}, {Key: "u", Value: 1}, {Key: "r", Value: 1}}},
	}
	if _, err := db.Collection(membersCollection).Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("bootstrap %s: create indexes: %w", membersCollection, err)
	}
	return nil
}
