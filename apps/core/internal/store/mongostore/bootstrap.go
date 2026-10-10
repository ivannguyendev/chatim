package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

func Bootstrap(ctx context.Context, db *mongo.Database) error {
	for _, name := range []string{messagesCollection, editsCollection, interactionsCollection, pinActionsCollection, membersCollection} {
		if err := ensureClustered(ctx, db, name); err != nil {
			return err
		}
	}
	for _, name := range []string{roomsCollection, reconcilerStateCollection, hiddenCollection} {
		if err := createCollection(ctx, db, name); err != nil {
			return err
		}
	}
	indexes := []struct {
		coll   string
		models []mongo.IndexModel
	}{
		{membersCollection, memberIndexes()},
		{roomsCollection, roomIndexes()},
		{editsCollection, roomTimeIndexes()},
		{hiddenCollection, hiddenIndexes()},
		{interactionsCollection, interactionIndexes()},
		{pinActionsCollection, roomTimeIndexes()},
	}
	for _, ix := range indexes {
		if err := ensureIndexes(ctx, db, ix.coll, ix.models); err != nil {
			return err
		}
	}
	return ensureFeedAnchor(ctx, db)
}

func ensureFeedAnchor(ctx context.Context, db *mongo.Database) error {
	majority := options.Collection().
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority())
	state := db.Collection(reconcilerStateCollection, majority)
	keepOrNow := bson.D{{Key: "$ifNull", Value: bson.A{"$cluster_time", "$$CLUSTER_TIME"}}}
	update := mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "cluster_time", Value: keepOrNow}}}}}
	filter := bson.D{{Key: "_id", Value: changesFeedID}}
	if _, err := state.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true)); err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("bootstrap %s: anchor change feed: %w", reconcilerStateCollection, err)
	}
	return nil
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

func memberIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{
			{Key: "room_id", Value: 1}, {Key: "state", Value: 1}, {Key: "role", Value: 1},
			{Key: "priority", Value: -1}, {Key: "joined_at", Value: 1}, {Key: "user_id", Value: 1},
		}},
		{Keys: bson.D{{Key: "tenant", Value: 1}, {Key: "user_id", Value: 1}, {Key: "state", Value: 1}, {Key: "room_id", Value: 1}}},
	}
}

func roomIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "activity_bucket", Value: 1}}}, {Keys: bson.D{{Key: "created_at", Value: 1}}}}
}

func roomTimeIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "created_at", Value: 1}}}}
}

func interactionIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "message_key", Value: 1}, {Key: "kind", Value: 1}, {Key: "state", Value: 1}, {Key: "value", Value: 1}}},
		{Keys: bson.D{{Key: "tenant", Value: 1}, {Key: "actor_id", Value: 1}, {Key: "kind", Value: 1}, {Key: "state", Value: 1}, {Key: "updated_at", Value: -1}}},
		{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "kind", Value: 1}, {Key: "updated_at", Value: 1}}},
	}
}

func hiddenIndexes() []mongo.IndexModel {
	keys := bson.D{{Key: "user_id", Value: 1}, {Key: "room_id", Value: 1}, {Key: "thread_root", Value: 1}, {Key: "seq", Value: 1}}
	return []mongo.IndexModel{
		{Keys: keys, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "created_at", Value: 1}}},
	}
}

func ensureIndexes(ctx context.Context, db *mongo.Database, coll string, models []mongo.IndexModel) error {
	if _, err := db.Collection(coll).Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("bootstrap %s: create indexes: %w", coll, err)
	}
	return nil
}
