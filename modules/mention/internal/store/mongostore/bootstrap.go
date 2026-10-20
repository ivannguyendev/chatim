package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	mentionsCollection   = "mentions"
	roomsCollection      = "mention_rooms"
	userGroupsCollection = "mention_user_groups"
	groupsCollection     = "mention_groups"
	mentionReadIndex     = "room_id_1_target_1_thread_root_1_seq_1"
)

func Bootstrap(ctx context.Context, db *mongo.Database) error {
	for _, name := range []string{mentionsCollection, userGroupsCollection, groupsCollection} {
		if err := ensureClustered(ctx, db, name); err != nil {
			return err
		}
	}
	if err := createCollection(ctx, db, roomsCollection); err != nil {
		return err
	}
	if err := ensureIndexes(ctx, db, mentionsCollection, mentionIndexes()); err != nil {
		return err
	}
	if err := ensureIndexes(ctx, db, userGroupsCollection, []mongo.IndexModel{{Keys: groupMembersIndexKeys()}}); err != nil {
		return err
	}
	return dropIndex(ctx, db.Collection(mentionsCollection), mentionReadIndex)
}

func mentionIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenant", Value: 1}, {Key: "target", Value: 1}, {Key: "state", Value: 1}, {Key: "created_at", Value: -1}, {Key: "message_key", Value: -1}}},
		{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "kind", Value: 1}, {Key: "created_at", Value: -1}, {Key: "message_key", Value: -1}}},
		{Keys: bson.D{{Key: "message_key", Value: 1}}},
	}
}

func groupMembersIndexKeys() bson.D {
	return bson.D{{Key: "tenant", Value: 1}, {Key: "groups", Value: 1}, {Key: "user_id", Value: 1}}
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
	if err := db.CreateCollection(ctx, name, opts...); err != nil && !hasCode(err, namespaceExistsCode) {
		return fmt.Errorf("bootstrap %s: create collection: %w", name, err)
	}
	return nil
}

func ensureIndexes(ctx context.Context, db *mongo.Database, coll string, models []mongo.IndexModel) error {
	if _, err := db.Collection(coll).Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("bootstrap %s: create indexes: %w", coll, err)
	}
	return nil
}

func dropIndex(ctx context.Context, coll *mongo.Collection, name string) error {
	if err := coll.Indexes().DropOne(ctx, name); err != nil && !hasCode(err, indexNotFoundCode) {
		return fmt.Errorf("bootstrap %s: drop index %s: %w", coll.Name(), name, err)
	}
	return nil
}
