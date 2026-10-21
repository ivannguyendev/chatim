package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/pkg/mongoindex"
)

func Bootstrap(ctx context.Context, db *mongo.Database, reporters ...mongoindex.Reporter) error {
	for _, name := range []string{messagesCollection, editsCollection, interactionsCollection, pinActionsCollection, membersCollection, roomDMsCollection, replyThreadsCollection} {
		if err := ensureClustered(ctx, db, name); err != nil {
			return err
		}
	}
	for _, name := range []string{roomsCollection, hiddenCollection, sweeperStateCollection} {
		if err := createCollection(ctx, db, name); err != nil {
			return err
		}
	}
	return mongoindex.Ensure(ctx, db, Indexes(), reporters...)
}

func ensureClustered(ctx context.Context, db *mongo.Database, name string) error {
	opts := options.CreateCollection().
		SetClusteredIndex(bson.D{{Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "unique", Value: true}}).
		SetStorageEngine(bson.D{{Key: "wiredTiger", Value: bson.D{{Key: "configString", Value: "block_compressor=zstd"}}}})
	if err := createCollection(ctx, db, name, opts); err != nil {
		return err
	}
	specs, err := db.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: name}})
	if err != nil {
		return fmt.Errorf("bootstrap %s: list collections: %w", name, err)
	}
	if len(specs) != 1 || !clusteredOnID(specs[0].Options) {
		return fmt.Errorf("bootstrap %s: %w", name, ErrNotClustered)
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
