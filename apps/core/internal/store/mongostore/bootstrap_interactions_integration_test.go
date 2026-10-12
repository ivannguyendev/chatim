package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestBootstrapCreatesInteractionAndPinCollections(t *testing.T) {
	db := itDatabase(t, itClient(t))
	for i := range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap #%d: %v", i+1, err)
		}
	}
	assertClusteredLayout(t, db, interactionsCollection)
	assertClusteredLayout(t, db, pinActionsCollection)
	got := indexKeys(t, db.Collection(interactionsCollection))
	for _, pattern := range []string{
		"message_key:1,kind:1,state:1,value:1",
		"tenant:1,actor_id:1,kind:1,state:1,updated_at:-1,message_key:-1",
		"room_id:1,kind:1,updated_at:1",
	} {
		if !hasIndex(got, pattern, false) {
			t.Fatalf("message_interactions indexes = %v, want non-unique %s", got, pattern)
		}
	}
	if got := indexKeys(t, db.Collection(pinActionsCollection)); !hasIndex(got, "room_id:1,created_at:1", false) {
		t.Fatalf("pin_actions indexes = %v, want non-unique room_id:1,created_at:1", got)
	}
	names, err := db.ListCollectionNames(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("ListCollectionNames: %v", err)
	}
	if slices.Contains(names, "reactions") {
		t.Fatalf("collections %v still hold reactions", names)
	}
}
