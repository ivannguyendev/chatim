package mongostore

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func feedStateFields(t *testing.T, db *mongo.Database) []string {
	t.Helper()
	raw, err := db.Collection(reconcilerStateCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: changesFeedID}}).Raw()
	if err != nil {
		t.Fatalf("load feed state: %v", err)
	}
	return fieldNames(t, raw)
}

func TestFeedAnchorUsesFullFieldNames(t *testing.T) {
	s, db := itStore(t, itClient(t))
	if got, want := feedStateFields(t, db), []string{"_id", "cluster_time"}; !slices.Equal(got, want) {
		t.Fatalf("anchor fields = %v, want %v", got, want)
	}
	cur, err := NewFeed(db).Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = cur.Close(context.Background()) })
	if res := s.Insert(t.Context(), []domain.Message{sampleMessage()}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert = %+v, want inserted", res)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	c, err := cur.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if err := cur.Confirm(t.Context(), c.Position); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	got := feedStateFields(t, db)
	slices.Sort(got)
	if want := []string{"_id", "cluster_time", "resume_token"}; !slices.Equal(got, want) {
		t.Fatalf("confirmed fields = %v, want %v", got, want)
	}
}

func TestForgetClearsThePosition(t *testing.T) {
	_, db := itStore(t, itClient(t))
	state := db.Collection(reconcilerStateCollection)
	if _, err := state.InsertOne(t.Context(), bson.D{{Key: "_id", Value: "messages"}, {Key: "at", Value: bson.Timestamp{T: 1, I: 1}}}); err != nil {
		t.Fatalf("insert an unrelated position: %v", err)
	}
	if err := NewFeed(db).Forget(t.Context()); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	ids, err := state.Distinct(t.Context(), "_id", bson.D{}).Raw()
	if err != nil {
		t.Fatalf("Distinct: %v", err)
	}
	if vals, _ := ids.Values(); len(vals) != 1 || vals[0].StringValue() != "messages" {
		t.Fatalf("reconciler_state ids after Forget = %s, want only the unrelated doc", ids)
	}
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if at := feedState(t, db).At; at.T <= 1 {
		t.Fatalf("anchor after Forget = %+v, want a fresh cluster time", at)
	}
}
