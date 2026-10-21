package mongostore

import (
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/mongoindex"
)

const membersUserIndex = "tenant_1_user_id_1_state_1_room_id_1_joined_at_1_cleared_at_1_read_seq_1_pinned_at_1_archived_at_1_ver_1"

var replacedIndexes = []mongoindex.Replacement{
	{Collection: membersCollection, Old: "tenant_1_user_id_1_state_1_room_id_1_joined_at_1_cleared_at_1_read_seq_1", New: membersUserIndex},
	{Collection: membersCollection, Old: "tenant_1_user_id_1_state_1_room_id_1_joined_at_1_cleared_at_1_read_seq_1_pinned_at_1_archived_at_1", New: membersUserIndex},
	{Collection: interactionsCollection, Old: "message_key_1_kind_1_state_1_value_1", New: "message_key_1_kind_1_state_1_value_1_updated_at_-1_actor_id_1"},
}

func Indexes() mongoindex.Catalog {
	declared := []mongoindex.Collection{
		{Name: messagesCollection},
		{Name: editsCollection, Indexes: roomTimeIndexes()},
		{Name: interactionsCollection, Indexes: interactionIndexes()},
		{Name: pinActionsCollection, Indexes: roomTimeIndexes()},
		{Name: membersCollection, Indexes: memberIndexes()},
		{Name: roomDMsCollection},
		{Name: replyThreadsCollection, Indexes: []mongo.IndexModel{{Keys: replyThreadsByRecent}}},
		{Name: roomsCollection, Indexes: roomIndexes()},
		{Name: hiddenCollection, Indexes: hiddenIndexes()},
		{Name: sweeperStateCollection},
	}
	pending := statePendingCollections()
	for i, c := range declared {
		if pending[c.Name] {
			declared[i].Indexes = append(slices.Clone(c.Indexes), statePendingModel())
		}
	}
	return mongoindex.Catalog{Collections: declared, Replacements: slices.Clone(replacedIndexes)}
}

func statePendingCollections() map[string]bool {
	out := map[string]bool{}
	for _, c := range store.AllStateCollections() {
		out[c.String()] = true
	}
	return out
}

func statePendingModel() mongo.IndexModel {
	return mongo.IndexModel{
		Keys:    bson.D{{Key: stateAtField, Value: 1}, {Key: "_id", Value: 1}},
		Options: options.Index().SetName(statePendingIndex).SetPartialFilterExpression(bson.D{stateExists}),
	}
}

func memberIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{
			{Key: "room_id", Value: 1}, {Key: "state", Value: 1}, {Key: "role", Value: 1},
			{Key: "priority", Value: -1}, {Key: "joined_at", Value: 1}, {Key: "user_id", Value: 1},
		}},
		{Keys: bson.D{
			{Key: "tenant", Value: 1}, {Key: "user_id", Value: 1}, {Key: "state", Value: 1}, {Key: "room_id", Value: 1},
			{Key: "joined_at", Value: 1}, {Key: "cleared_at", Value: 1}, {Key: "read_seq", Value: 1},
			{Key: "pinned_at", Value: 1}, {Key: "archived_at", Value: 1}, {Key: "ver", Value: 1},
		}},
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
		{Keys: bson.D{
			{Key: "message_key", Value: 1}, {Key: "kind", Value: 1}, {Key: "state", Value: 1}, {Key: "value", Value: 1},
			{Key: "updated_at", Value: -1}, {Key: "actor_id", Value: 1},
		}},
		{Keys: bson.D{{Key: "tenant", Value: 1}, {Key: "actor_id", Value: 1}, {Key: "kind", Value: 1}, {Key: "state", Value: 1}, {Key: "updated_at", Value: -1}, {Key: "message_key", Value: -1}}},
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
