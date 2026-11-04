package mongoindex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func itDatabase(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("CHATIM_IT_MONGO_URI")
	if uri == "" {
		t.Skip("set CHATIM_IT_MONGO_URI to run")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	db := client.Database("chatim_index_it_" + hex.EncodeToString(suffix))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.Drop(ctx); err != nil {
			t.Errorf("drop %s: %v", db.Name(), err)
		}
		_ = client.Disconnect(ctx)
	})
	return db
}

func TestIsEmptyLooksForAnyDocument(t *testing.T) {
	db := itDatabase(t)
	coll := db.Collection("probe")
	if err := db.CreateCollection(t.Context(), "probe"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if empty, err := isEmpty(t.Context(), coll); err != nil || !empty {
		t.Fatalf("isEmpty(new) = %v, %v; want true", empty, err)
	}
	if _, err := coll.InsertOne(t.Context(), bson.D{{Key: "_id", Value: 1}}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if empty, err := isEmpty(t.Context(), coll); err != nil || empty {
		t.Fatalf("isEmpty(one doc) = %v, %v; want false", empty, err)
	}
	if _, err := coll.DeleteOne(t.Context(), bson.D{{Key: "_id", Value: 1}}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if empty, err := isEmpty(t.Context(), coll); err != nil || !empty {
		t.Fatalf("isEmpty(after delete) = %v, %v; want true", empty, err)
	}
}

func TestEnsureBuildsOnlyOnAnEmptyCollection(t *testing.T) {
	db := itDatabase(t)
	cat := Catalog{Collections: []Collection{{Name: "full", Indexes: []mongo.IndexModel{{Keys: bson.D{{Key: "a", Value: 1}}}}}, {Name: "empty", Indexes: []mongo.IndexModel{{Keys: bson.D{{Key: "a", Value: 1}}}}}}}
	for _, name := range []string{"full", "empty"} {
		if err := db.CreateCollection(t.Context(), name); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if _, err := db.Collection("full").InsertOne(t.Context(), bson.D{{Key: "a", Value: 1}}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := Ensure(t.Context(), db, cat); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	reports, err := Inspect(t.Context(), db, cat)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !reports[0].Stale() || reports[1].Stale() {
		t.Fatalf("reports = %+v, want full still missing a_1 and empty built", reports)
	}
}
