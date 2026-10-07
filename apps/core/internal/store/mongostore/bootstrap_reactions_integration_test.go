package mongostore

import "testing"

func TestBootstrapCreatesReactionAndPinCollections(t *testing.T) {
	db := itDatabase(t, itClient(t))
	for i := range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap #%d: %v", i+1, err)
		}
	}
	assertClusteredLayout(t, db, reactionsCollection)
	assertClusteredLayout(t, db, pinActionsCollection)
	if got := indexKeys(t, db.Collection(reactionsCollection)); !hasIndex(got, "message_key:1,emoji:1", false) || !hasIndex(got, "room_id:1,updated_at:1", false) {
		t.Fatalf("reactions indexes = %v, want non-unique message_key:1,emoji:1 and room_id:1,updated_at:1", got)
	}
	if got := indexKeys(t, db.Collection(pinActionsCollection)); !hasIndex(got, "room_id:1,created_at:1", false) {
		t.Fatalf("pin_actions indexes = %v, want non-unique room_id:1,created_at:1", got)
	}
}
