package mongostore

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestBootstrapDropsReplacedIndexes(t *testing.T) {
	db := itDatabase(t, itClient(t))
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	old := map[string]bson.D{
		membersCollection: {
			{Key: "tenant", Value: 1}, {Key: "user_id", Value: 1}, {Key: "state", Value: 1}, {Key: "room_id", Value: 1},
			{Key: "joined_at", Value: 1}, {Key: "cleared_at", Value: 1}, {Key: "read_seq", Value: 1},
		},
		interactionsCollection: {{Key: "message_key", Value: 1}, {Key: "kind", Value: 1}, {Key: "state", Value: 1}, {Key: "value", Value: 1}},
	}
	for coll, keys := range old {
		if _, err := db.Collection(coll).Indexes().CreateOne(t.Context(), mongo.IndexModel{Keys: keys}); err != nil {
			t.Fatalf("create old %s index: %v", coll, err)
		}
	}
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap over old indexes: %v", err)
	}
	gone := map[string]string{
		membersCollection:      "tenant:1,user_id:1,state:1,room_id:1,joined_at:1,cleared_at:1,read_seq:1",
		interactionsCollection: "message_key:1,kind:1,state:1,value:1",
	}
	for coll, pattern := range gone {
		if got := indexKeys(t, db.Collection(coll)); hasIndex(got, pattern, false) {
			t.Fatalf("%s indexes = %v, want the replaced %s dropped", coll, got, pattern)
		}
	}
	assertMemberIndexes(t, db)
}
