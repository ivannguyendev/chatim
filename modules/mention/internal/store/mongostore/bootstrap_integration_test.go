package mongostore

import (
	"errors"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func indexNames(t *testing.T, db *mongo.Database, coll string) []string {
	t.Helper()
	specs, err := db.Collection(coll).Indexes().ListSpecifications(t.Context())
	if err != nil {
		t.Fatalf("list indexes of %s: %v", coll, err)
	}
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	slices.Sort(names)
	return names
}

func TestBootstrapKeepsCoreMentionsAndDropsTheReadIndex(t *testing.T) {
	db := itDatabase(t, itClient(t))
	if err := ensureClustered(t.Context(), db, mentionsCollection); err != nil {
		t.Fatalf("seed clustered mentions: %v", err)
	}
	old := mongo.IndexModel{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "target", Value: 1}, {Key: "thread_root", Value: 1}, {Key: "seq", Value: 1}}}
	if err := ensureIndexes(t.Context(), db, mentionsCollection, append(mentionIndexes(), old)); err != nil {
		t.Fatalf("seed core indexes: %v", err)
	}
	for range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap: %v", err)
		}
	}
	want := []string{"_id_", "message_key_1", "room_id_1_kind_1_created_at_-1_message_key_-1", "tenant_1_target_1_state_1_created_at_-1_message_key_-1"}
	if got := indexNames(t, db, mentionsCollection); !slices.Equal(got, want) {
		t.Fatalf("mentions indexes = %v, want %v", got, want)
	}
	if got, want := indexNames(t, db, userGroupsCollection), []string{"_id_", "tenant_1_groups_1_user_id_1"}; !slices.Equal(got, want) {
		t.Fatalf("user groups indexes = %v, want %v", got, want)
	}
	for _, name := range []string{groupsCollection, userGroupsCollection} {
		if err := ensureClustered(t.Context(), db, name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestBootstrapRefusesAnUnclusteredMentions(t *testing.T) {
	db := itDatabase(t, itClient(t))
	if err := db.CreateCollection(t.Context(), mentionsCollection); err != nil {
		t.Fatalf("create plain mentions: %v", err)
	}
	if err := Bootstrap(t.Context(), db); !errors.Is(err, ErrNotClustered) {
		t.Fatalf("Bootstrap = %v, want ErrNotClustered", err)
	}
}
