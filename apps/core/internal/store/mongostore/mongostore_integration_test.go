package mongostore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

const itMongoURIEnv = "CHATIM_IT_MONGO_URI"

func itClient(t *testing.T) *mongo.Client {
	t.Helper()
	uri := os.Getenv(itMongoURIEnv)
	if uri == "" {
		t.Skip("set CHATIM_IT_MONGO_URI to run")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := client.Ping(t.Context(), readpref.Primary()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return client
}

func itDatabase(t *testing.T, client *mongo.Client) *mongo.Database {
	t.Helper()
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	db := client.Database("chatim_it_" + hex.EncodeToString(suffix))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.Drop(ctx); err != nil {
			t.Errorf("drop %s: %v", db.Name(), err)
		}
	})
	return db
}

func itStore(t *testing.T, client *mongo.Client) (*Store, *mongo.Database) {
	t.Helper()
	db := itDatabase(t, client)
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return New(db, Options{}), db
}

func TestMongoStoreContract(t *testing.T) {
	client := itClient(t)
	storetest.Run(t, func(t *testing.T) (store.Messages, store.Rooms) {
		s, _ := itStore(t, client)
		return s, s
	})
}
