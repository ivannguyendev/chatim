package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

func Bootstrap(ctx context.Context, db *mongo.Database) error {
	if err := ensureMessages(ctx, db); err != nil {
		return err
	}
	for _, name := range []string{roomsCollection, membersCollection, reconcilerStateCollection} {
		if err := createCollection(ctx, db, name); err != nil {
			return err
		}
	}
	if err := ensureIndexes(ctx, db, membersCollection, memberIndexes()); err != nil {
		return err
	}
	if err := ensureIndexes(ctx, db, roomsCollection, roomIndexes()); err != nil {
		return err
	}
	return ensureFeedAnchor(ctx, db)
}

func ensureFeedAnchor(ctx context.Context, db *mongo.Database) error {
	majority := options.Collection().
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority())
	state := db.Collection(reconcilerStateCollection, majority)
	start, err := feedStart(ctx, state)
	if err != nil {
		return err
	}
	keepOrStart := bson.D{{Key: "$ifNull", Value: bson.A{"$at", start}}}
	update := mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "at", Value: keepOrStart}}}}}
	filter := bson.D{{Key: "_id", Value: changesFeedID}}
	if _, err := state.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true)); err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("bootstrap %s: anchor change feed: %w", reconcilerStateCollection, err)
	}
	return nil
}

func feedStart(ctx context.Context, state *mongo.Collection) (any, error) {
	var old feedPosition
	err := state.FindOne(ctx, bson.D{{Key: "_id", Value: legacyMessagesFeedID}}).Decode(&old)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments) || (err == nil && old.At.IsZero()):
		return "$$CLUSTER_TIME", nil
	case err != nil:
		return nil, fmt.Errorf("bootstrap %s: read the messages feed position: %w", reconcilerStateCollection, err)
	default:
		return old.At, nil
	}
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

func memberIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "r", Value: 1}, {Key: "u", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "t", Value: 1}, {Key: "u", Value: 1}, {Key: "r", Value: 1}}},
	}
}

func roomIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "ab", Value: 1}}}, {Keys: bson.D{{Key: "ca", Value: 1}}}}
}

func ensureIndexes(ctx context.Context, db *mongo.Database, coll string, models []mongo.IndexModel) error {
	if _, err := db.Collection(coll).Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("bootstrap %s: create indexes: %w", coll, err)
	}
	return nil
}
