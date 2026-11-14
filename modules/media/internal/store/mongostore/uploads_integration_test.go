package mongostore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/storetest"
)

func itDatabase(t *testing.T) *mongo.Database {
	t.Helper()
	return itDatabaseWatched(t, nil)
}

func itDatabaseWatched(t *testing.T, monitor *event.CommandMonitor) *mongo.Database {
	t.Helper()
	uri := os.Getenv("CHATIM_IT_MONGO_URI")
	if uri == "" {
		t.Skip("set CHATIM_IT_MONGO_URI to run")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(monitor))
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
	for _, name := range []string{uploadsCollection, messageFilesCollection, roomAvatarsCollection} {
		specs, err := db.ListCollectionSpecifications(t.Context(), bson.D{{Key: "name", Value: name}})
		if err != nil || len(specs) != 1 {
			t.Fatalf("%s specs = %v, %v", name, specs, err)
		}
		if _, ok := specs[0].Options.Lookup("clusteredIndex").DocumentOK(); !ok {
			t.Fatalf("%s options = %v", name, specs[0].Options)
		}
	}
	plain := itDatabase(t)
	if err := plain.CreateCollection(t.Context(), uploadsCollection); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(t.Context(), plain); !errors.Is(err, ErrNotClustered) {
		t.Fatalf("plain collection err = %v", err)
	}
}

func TestUploadFactsAreStoredOnlyWhenGiven(t *testing.T) {
	db := itDatabase(t)
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	s := NewUploads(db)
	plain := storetest.Upload("acme/an/20261106/kkkk")
	facts := storetest.Upload("acme/an/20261106/llll")
	facts.Dim, facts.Dur = []int32{1920, 1080}, 95400
	hash := "sha256:" + strings.Repeat("ab", 32)
	for _, u := range []domain.Upload{plain, facts} {
		if err := s.Insert(t.Context(), u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Transition(t.Context(), plain.Key, domain.StateUploading, 1, domain.StateUploaded, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(t.Context(), facts.Key, domain.StateUploading, 1, domain.StateUploaded, hash); err != nil {
		t.Fatal(err)
	}
	raw := func(key string) bson.M {
		var doc bson.M
		if err := db.Collection(uploadsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: key}}).Decode(&doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	for _, field := range []string{"dim", "dur", "hash"} {
		if v, ok := raw(plain.Key)[field]; ok {
			t.Fatalf("plain upload has %s = %v", field, v)
		}
	}
	got := raw(facts.Key)
	if got["hash"] != hash || got["dur"] != int64(95400) || fmt.Sprint(got["dim"]) != "[1920 1080]" {
		t.Fatalf("facts doc = %v", got)
	}
}
