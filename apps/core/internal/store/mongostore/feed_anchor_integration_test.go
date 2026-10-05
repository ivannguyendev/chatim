package mongostore

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func setMessagesPosition(t *testing.T, db *mongo.Database, at bson.Timestamp) {
	t.Helper()
	filter := bson.D{{Key: "_id", Value: legacyMessagesFeedID}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "at", Value: at}}}}
	if _, err := db.Collection(reconcilerStateCollection).UpdateOne(t.Context(), filter, update, options.UpdateOne().SetUpsert(true)); err != nil {
		t.Fatalf("set the messages position: %v", err)
	}
}

func TestBootstrapCarriesTheMessagesPositionOverWithoutLosingWrites(t *testing.T) {
	s, db := itStore(t, itClient(t))
	old := feedState(t, db).At
	if _, err := db.Collection(reconcilerStateCollection).DeleteOne(t.Context(), bson.D{{Key: "_id", Value: changesFeedID}}); err != nil {
		t.Fatalf("drop the changes position: %v", err)
	}
	setMessagesPosition(t, db, old)
	m := sampleMessage()
	if res := s.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert = %+v, want inserted", res)
	}
	bootstrapTwice(t, db, feedPosition{At: old})
	setMessagesPosition(t, db, bson.Timestamp{T: old.T + 60, I: 1})
	bootstrapTwice(t, db, feedPosition{At: old})
	cur, err := NewFeed(db).Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = cur.Close(context.Background()) })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	c, err := cur.Next(ctx)
	if err != nil || c.Kind != store.MessageInserted || c.Msg.CID != m.CID {
		t.Fatalf("first change after the carry-over = %+v, %v; want the message written after the old position", c, err)
	}
}

func TestForgetClearsTheOldAndTheNewPosition(t *testing.T) {
	_, db := itStore(t, itClient(t))
	setMessagesPosition(t, db, bson.Timestamp{T: 1, I: 1})
	if err := NewFeed(db).Forget(t.Context()); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	n, err := db.Collection(reconcilerStateCollection).CountDocuments(t.Context(), bson.D{})
	if err != nil || n != 0 {
		t.Fatalf("reconciler_state holds %d documents after Forget (%v), want none", n, err)
	}
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if at := feedState(t, db).At; at.T <= 1 {
		t.Fatalf("anchor after Forget = %+v, want a fresh cluster time", at)
	}
}
