package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/pkg/mongoindex"
)

const (
	mentionsCollection   = "mentions"
	roomsCollection      = "mention_rooms"
	userGroupsCollection = "mention_user_groups"
	groupsCollection     = "mention_groups"
	mentionReadIndex     = "room_id_1_target_1_thread_root_1_seq_1"
)

func Indexes() mongoindex.Catalog {
	return mongoindex.Catalog{
		Collections: []mongoindex.Collection{
			{Name: mentionsCollection, Indexes: mentionIndexes()},
			{Name: roomsCollection},
			{Name: userGroupsCollection, Indexes: []mongo.IndexModel{{Keys: groupMembersIndexKeys()}}},
			{Name: groupsCollection},
		},
		Replacements: []mongoindex.Replacement{{Collection: mentionsCollection, Old: mentionReadIndex}},
	}
}

func Bootstrap(ctx context.Context, db *mongo.Database, reporters ...mongoindex.Reporter) error {
	for _, name := range []string{mentionsCollection, userGroupsCollection, groupsCollection} {
		if err := ensureClustered(ctx, db, name); err != nil {
			return err
		}
	}
	if err := createCollection(ctx, db, roomsCollection); err != nil {
		return err
	}
	return mongoindex.Ensure(ctx, db, Indexes(), reporters...)
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
