package mongostore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/modules/media/internal/store"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/storetest"
)

func itDatabase(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("CHATIM_IT_MONGO_URI")
	if uri == "" {
		t.Skip("set CHATIM_IT_MONGO_URI to run")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	db := client.Database("chatim_media_it_" + hex.EncodeToString(suffix))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = client.Disconnect(ctx)
	})
	return db
}

func TestUploadsContractOnMongo(t *testing.T) {
	storetest.RunUploads(t, func(t *testing.T) store.Uploads {
		db := itDatabase(t)
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatal(err)
		}
		return NewUploads(db)
	})
}

func TestBootstrapIsClusteredAndRerunnable(t *testing.T) {
	db := itDatabase(t)
	for range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatal(err)
		}
	}
	specs, err := db.ListCollectionSpecifications(t.Context(), bson.D{{Key: "name", Value: uploadsCollection}})
	if err != nil || len(specs) != 1 {
		t.Fatalf("specs = %v, %v", specs, err)
	}
	if _, ok := specs[0].Options.Lookup("clusteredIndex").DocumentOK(); !ok {
		t.Fatalf("options = %v", specs[0].Options)
	}
	plain := itDatabase(t)
	if err := plain.CreateCollection(t.Context(), uploadsCollection); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(t.Context(), plain); !errors.Is(err, ErrNotClustered) {
		t.Fatalf("plain collection err = %v", err)
	}
}
