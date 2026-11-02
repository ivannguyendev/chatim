package mongostore

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestSweepCheckpointStoredAsCheckpointAt(t *testing.T) {
	client := itClient(t)
	s, db := itStore(t, client)
	at := time.Date(2026, 11, 2, 10, 15, 30, 0, time.UTC)
	if err := s.SweepCheckpoints().Advance(t.Context(), store.StateMessages, at); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	var raw bson.M
	if err := db.Collection(sweeperStateCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: store.StateMessages.String()}}).Decode(&raw); err != nil {
		t.Fatalf("FindOne: %v", err)
	}
	if _, ok := raw["checkpoint_at"]; !ok {
		t.Fatalf("doc %v has no checkpoint_at", raw)
	}
	if _, ok := raw["checkpoint"]; ok {
		t.Fatalf("doc %v still has checkpoint", raw)
	}
	got, err := s.SweepCheckpoints().Get(t.Context(), store.StateMessages)
	if err != nil || !got.Equal(at) {
		t.Fatalf("Get = %v, %v; want %v", got, err, at)
	}
}

func TestSweepCheckpointOldFieldReadsAsNoCheckpoint(t *testing.T) {
	client := itClient(t)
	s, db := itStore(t, client)
	old := bson.D{{Key: "_id", Value: store.StateMessages.String()}, {Key: "checkpoint", Value: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}}
	if _, err := db.Collection(sweeperStateCollection).InsertOne(t.Context(), old); err != nil {
		t.Fatalf("InsertOne: %v", err)
	}
	got, err := s.SweepCheckpoints().Get(t.Context(), store.StateMessages)
	if err != nil || !got.IsZero() {
		t.Fatalf("Get = %v, %v; want zero time so the sweeper rescans", got, err)
	}
}
