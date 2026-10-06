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
	if got := indexKeys(t, db.Collection(reactionsCollection)); !hasIndex(got, "k:1,e:1", false) || !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("reactions indexes = %v, want non-unique k:1,e:1 and r:1,ts:1", got)
	}
	if got := indexKeys(t, db.Collection(pinActionsCollection)); !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("pin_actions indexes = %v, want non-unique r:1,ts:1", got)
	}
	if got := indexKeys(t, db.Collection(editsCollection)); !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("message_edits indexes = %v, want non-unique r:1,ts:1 after the rename", got)
	}
}
