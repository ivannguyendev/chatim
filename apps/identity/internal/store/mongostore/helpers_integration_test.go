package mongostore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const itMongoURIEnv = "CHATIM_IT_MONGO_URI"

func itURI(t *testing.T) string {
	t.Helper()
	uri := os.Getenv(itMongoURIEnv)
	if uri == "" {
		t.Skip("set CHATIM_IT_MONGO_URI to run")
	}
	return uri
}

func testDatabase(t *testing.T) *Store {
	t.Helper()
	uri := itURI(t)
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	s, err := Open(Options{
		URI:            uri,
		Database:       "identity_it_" + hex.EncodeToString(suffix),
		AppName:        "chatim-identity-it",
		RevocationKeep: time.Hour,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.db.Drop(ctx); err != nil {
			t.Errorf("drop %s: %v", s.db.Name(), err)
		}
		s.Close()
	})
	return s
}

func insertDoc(t *testing.T, s *Store, collection string, doc bson.D) {
	t.Helper()
	if _, err := s.db.Collection(collection).InsertOne(t.Context(), doc); err != nil {
		t.Fatalf("insert into %s: %v", collection, err)
	}
}
