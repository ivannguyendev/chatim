package mongostore

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func collectionOptions(t *testing.T, db *mongo.Database, name string) bson.Raw {
	t.Helper()
	specs, err := db.ListCollectionSpecifications(t.Context(), bson.D{{Key: "name", Value: name}})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("collection %q: found %d, want 1", name, len(specs))
	}
	return specs[0].Options
}

func indexKeys(t *testing.T, coll *mongo.Collection) map[string]bool {
	t.Helper()
	specs, err := coll.Indexes().ListSpecifications(t.Context())
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	out := make(map[string]bool, len(specs))
	for _, s := range specs {
		out[keyPattern(t, s.KeysDocument)] = s.Unique != nil && *s.Unique
	}
	return out
}

func keyPattern(t *testing.T, keys bson.Raw) string {
	t.Helper()
	elems, err := keys.Elements()
	if err != nil {
		t.Fatalf("index keys: %v", err)
	}
	parts := make([]string, len(elems))
	for i, e := range elems {
		dir, _ := e.Value().AsInt64OK()
		parts[i] = fmt.Sprintf("%s:%d", e.Key(), dir)
	}
	return strings.Join(parts, ",")
}

func assertMessagesLayout(t *testing.T, db *mongo.Database) {
	t.Helper()
	opts := collectionOptions(t, db, messagesCollection)
	if v, ok := opts.Lookup("clusteredIndex", "key", "_id").AsInt64OK(); !ok || v != 1 {
		t.Fatalf("messages options %s: want clusteredIndex key {_id: 1}", opts)
	}
	if u, ok := opts.Lookup("clusteredIndex", "unique").BooleanOK(); !ok || !u {
		t.Fatalf("messages options %s: want a unique clustered index", opts)
	}
	if c, _ := opts.Lookup("storageEngine", "wiredTiger", "configString").StringValueOK(); c != "block_compressor=zstd" {
		t.Fatalf("messages configString = %q, want block_compressor=zstd", c)
	}
}

func assertMemberIndexes(t *testing.T, db *mongo.Database) {
	t.Helper()
	got := indexKeys(t, db.Collection(membersCollection))
	want := map[string]bool{"_id:1": false, "r:1,u:1": true, "t:1,u:1,r:1": false}
	for k, unique := range want {
		if u, ok := got[k]; !ok || u != unique {
			t.Fatalf("members indexes = %v, want %s with unique=%v", got, k, unique)
		}
	}
}

func TestBootstrapIsIdempotent(t *testing.T) {
	db := itDatabase(t, itClient(t))
	for i := range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap #%d: %v", i+1, err)
		}
	}
	s := New(db, Options{})
	m := sampleMessage()
	if res := s.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert = %+v, want inserted", res)
	}
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap with data: %v", err)
	}
	assertMessagesLayout(t, db)
	assertMemberIndexes(t, db)
	collectionOptions(t, db, roomsCollection)
	collectionOptions(t, db, reconcilerStateCollection)
	got, err := s.Find(t.Context(), m.Room, []store.MsgKey{store.KeyOf(m)})
	if err != nil || len(got) != 1 || got[0].CID != m.CID {
		t.Fatalf("Find after re-bootstrap = %+v, %v; want the inserted message", got, err)
	}
}

func TestBootstrapRejectsUnclusteredMessages(t *testing.T) {
	db := itDatabase(t, itClient(t))
	if err := db.CreateCollection(t.Context(), messagesCollection); err != nil {
		t.Fatalf("create plain messages: %v", err)
	}
	err := Bootstrap(t.Context(), db)
	if !errors.Is(err, ErrNotClustered) {
		t.Fatalf("Bootstrap error = %v, want ErrNotClustered", err)
	}
	names, err := db.ListCollectionNames(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if !slices.Equal(names, []string{messagesCollection}) {
		t.Fatalf("collections = %v, want only the pre-existing messages", names)
	}
}
